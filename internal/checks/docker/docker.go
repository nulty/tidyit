// Package docker is a bundled plugin: it ships with tidy but only runs when
// the docker CLI is installed and the daemon is reachable.
package docker

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"tidy/internal/engine"
	"tidy/internal/fsx"
	"tidy/pkg/manifest"
	"tidy/pkg/report"
)

// Plugin returns the docker plugin.
func Plugin() *engine.Plugin {
	return &engine.Plugin{
		Manifest: manifest.Manifest{
			ID: "docker", Name: "Docker", Version: "0.1.0", Protocol: manifest.ProtocolVersion,
			Requires: []string{"docker"},
			Checks: []manifest.CheckSpec{
				{
					ID:    "unused-images",
					Title: "Images not used by any container",
					Hint:  "remove one: docker image rm <id>   all unused: docker image prune -a",
					Options: map[string]manifest.Option{
						"min_age_days": {Type: manifest.TypeInteger, Default: 0, Min: manifest.Ptr(0),
							Description: "Ignore images created more recently than this"},
						"ignore": {Type: manifest.TypeStrings, Default: []string{},
							Description: "Image names to never flag, as globs, e.g. postgres:* or *:latest"},
					},
				},
				{
					ID:    "unused-networks",
					Title: "Networks with no containers",
					Hint:  "docker network prune",
				},
				{
					ID:    "unattached-volumes",
					Title: "Volumes not attached to any container",
					Hint:  "volumes can hold data, check first: docker volume inspect <name>",
					Options: map[string]manifest.Option{
						"include_anonymous": {Type: manifest.TypeBoolean, Default: true,
							Description: "Include anonymous volumes (created without a name)"},
					},
				},
			},
		},
		Checks: map[string]engine.CheckFunc{
			"unused-images":      unusedImages,
			"unused-networks":    unusedNetworks,
			"unattached-volumes": unattachedVolumes,
		},
		Probe: func(ctx context.Context) error {
			if _, err := docker(ctx, "info", "--format", "{{.ServerVersion}}"); err != nil {
				return engine.Skip("docker daemon not reachable (is it running, and are you in the docker group?)")
			}
			return nil
		},
	}
}

func docker(ctx context.Context, args ...string) ([]byte, error) {
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return nil, fmt.Errorf("docker %s: %s", args[0], msg)
		}
		return nil, fmt.Errorf("docker %s: %w", args[0], err)
	}
	return out, nil
}

// jsonLines decodes `--format '{{json .}}'` output, one object per line.
func jsonLines[T any](data []byte) ([]T, error) {
	var out []T
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var v T
		if err := json.Unmarshal(line, &v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, sc.Err()
}

type usage struct {
	images   map[string]bool // full sha256 image ids
	networks map[string]bool // network names
}

// containerUsage lists what every container (running or stopped) uses.
func containerUsage(ctx context.Context) (usage, error) {
	u := usage{images: map[string]bool{}, networks: map[string]bool{}}
	ids, err := docker(ctx, "ps", "-aq", "--no-trunc")
	if err != nil {
		return u, err
	}
	list := strings.Fields(string(ids))
	if len(list) == 0 {
		return u, nil
	}
	args := append([]string{"inspect", "--format", "{{.Image}}|{{range $k, $v := .NetworkSettings.Networks}}{{$k}} {{end}}"}, list...)
	out, err := docker(ctx, args...)
	if err != nil {
		return u, err
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		img, nets, _ := strings.Cut(line, "|")
		u.images[img] = true
		for _, n := range strings.Fields(nets) {
			u.networks[n] = true
		}
	}
	return u, nil
}

type imageRow struct {
	ID, Repository, Tag, CreatedAt, Size string
}

func unusedImages(ctx context.Context, in engine.Input) (engine.Outcome, error) {
	u, err := containerUsage(ctx)
	if err != nil {
		return engine.Outcome{}, err
	}
	raw, err := docker(ctx, "image", "ls", "--no-trunc", "--format", "{{json .}}")
	if err != nil {
		return engine.Outcome{}, err
	}
	rows, err := jsonLines[imageRow](raw)
	if err != nil {
		return engine.Outcome{}, fmt.Errorf("reading docker image list: %w", err)
	}

	// One image id can have several tags; group them.
	type image struct {
		id      string
		refs    []string
		created time.Time
		size    int64
	}
	byID := map[string]*image{}
	var order []string
	for _, r := range rows {
		im := byID[r.ID]
		if im == nil {
			created, _ := time.Parse("2006-01-02 15:04:05 -0700 MST", r.CreatedAt)
			im = &image{id: r.ID, created: created, size: fsx.ParseDecimalSize(r.Size)}
			byID[r.ID] = im
			order = append(order, r.ID)
		}
		if r.Repository != "<none>" {
			im.refs = append(im.refs, r.Repository+":"+r.Tag)
		}
	}

	ignore := in.Options.Strings("ignore")
	var cutoff time.Time
	if d := in.Options.Int("min_age_days"); d > 0 {
		cutoff = time.Now().AddDate(0, 0, -d)
	}
	var out engine.Outcome
	var total int64
	for _, id := range order {
		im := byID[id]
		// Show the shortest name first (e.g. "app:latest" before the
		// registry-qualified copy of the same image).
		sort.Slice(im.refs, func(i, j int) bool {
			if len(im.refs[i]) != len(im.refs[j]) {
				return len(im.refs[i]) < len(im.refs[j])
			}
			return im.refs[i] < im.refs[j]
		})
		if u.images[id] || (!cutoff.IsZero() && im.created.After(cutoff)) || ignored(ignore, im.refs) {
			continue
		}
		short := strings.TrimPrefix(id, "sha256:")
		if len(short) > 12 {
			short = short[:12]
		}
		it := report.Item{ID: id, Size: im.size, Detail: "created " + fsx.Age(im.created)}
		if len(im.refs) == 0 {
			// Dangling: an old build replaced by a newer one with the same tag.
			it.Label = "<untagged> " + short
			it.Confidence = report.ConfidenceSafe
			it.Action = &report.Action{Command: "docker image rm " + short}
		} else {
			it.Label = im.refs[0]
			if n := len(im.refs) - 1; n > 0 {
				it.Label += fmt.Sprintf(" (+%d more tags)", n)
			}
			it.Confidence = report.ConfidenceReview
			it.Action = &report.Action{Command: "docker image rm " + strings.Join(im.refs, " ")}
		}
		total += im.size
		out.Items = append(out.Items, it)
	}
	sort.SliceStable(out.Items, func(i, j int) bool { return out.Items[i].Size > out.Items[j].Size })
	if n := len(out.Items); n > 0 {
		out.Summary = fmt.Sprintf("%d images, about %s (layers shared with other images are counted once per image)", n, fsx.Human(total))
	}
	return out, nil
}

func ignored(globs, refs []string) bool {
	for _, ref := range refs {
		for _, g := range globs {
			if ok, _ := filepath.Match(g, ref); ok {
				return true
			}
		}
	}
	return false
}

type networkRow struct {
	Name, Driver, Labels, CreatedAt string
}

func unusedNetworks(ctx context.Context, _ engine.Input) (engine.Outcome, error) {
	u, err := containerUsage(ctx)
	if err != nil {
		return engine.Outcome{}, err
	}
	raw, err := docker(ctx, "network", "ls", "--format", "{{json .}}")
	if err != nil {
		return engine.Outcome{}, err
	}
	rows, err := jsonLines[networkRow](raw)
	if err != nil {
		return engine.Outcome{}, fmt.Errorf("reading docker network list: %w", err)
	}
	var out engine.Outcome
	for _, n := range rows {
		switch n.Name {
		case "bridge", "host", "none":
			continue
		}
		if u.networks[n.Name] {
			continue
		}
		it := report.Item{ID: n.Name, Label: n.Name, Confidence: report.ConfidenceSafe,
			Action: &report.Action{Command: "docker network rm " + n.Name}}
		if p := label(n.Labels, "com.docker.compose.project"); p != "" {
			it.Detail = "compose project " + p
		}
		out.Items = append(out.Items, it)
	}
	if n := len(out.Items); n > 0 {
		out.Summary = fmt.Sprintf("%d networks", n)
	}
	return out, nil
}

type volumeRow struct {
	Name, Labels string
}

func unattachedVolumes(ctx context.Context, in engine.Input) (engine.Outcome, error) {
	raw, err := docker(ctx, "volume", "ls", "--filter", "dangling=true", "--format", "{{json .}}")
	if err != nil {
		return engine.Outcome{}, err
	}
	rows, err := jsonLines[volumeRow](raw)
	if err != nil {
		return engine.Outcome{}, fmt.Errorf("reading docker volume list: %w", err)
	}
	var out engine.Outcome
	anon := 0
	for _, v := range rows {
		isAnon := strings.Contains(v.Labels, "com.docker.volume.anonymous")
		if isAnon && !in.Options.Bool("include_anonymous") {
			continue
		}
		it := report.Item{ID: v.Name, Label: v.Name, Confidence: report.ConfidenceReview,
			Action: &report.Action{Command: "docker volume rm " + v.Name}}
		switch {
		case isAnon:
			anon++
			it.Label = v.Name[:min(12, len(v.Name))] + "…"
			it.Detail = "anonymous"
		case label(v.Labels, "com.docker.compose.project") != "":
			it.Detail = "compose project " + label(v.Labels, "com.docker.compose.project")
		}
		out.Items = append(out.Items, it)
	}
	if n := len(out.Items); n > 0 {
		out.Summary = fmt.Sprintf("%d volumes (%d anonymous)", n, anon)
	}
	return out, nil
}

// label reads one key from docker's "k=v,k2=v2" label string.
func label(labels, key string) string {
	for _, kv := range strings.Split(labels, ",") {
		if k, v, ok := strings.Cut(kv, "="); ok && k == key {
			return v
		}
	}
	return ""
}
