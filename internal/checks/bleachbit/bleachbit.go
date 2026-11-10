// Package bleachbit is a bundled plugin that previews what BleachBit would
// clean. It only ever runs `bleachbit --preview`, which deletes nothing.
package bleachbit

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"tidy/internal/engine"
	"tidy/internal/fsx"
	"tidy/pkg/manifest"
	"tidy/pkg/report"
)

// Plugin returns the bleachbit plugin.
func Plugin() *engine.Plugin {
	return &engine.Plugin{
		Manifest: manifest.Manifest{
			ID: "bleachbit", Name: "BleachBit", Version: "0.1.0", Protocol: manifest.ProtocolVersion,
			Requires: []string{"bleachbit"},
			Checks: []manifest.CheckSpec{{
				ID:          "preview",
				Title:       "BleachBit preview",
				Description: "What BleachBit would delete with your selection, grouped by folder. Nothing is deleted.",
				Timeout:     "120s",
				Options: map[string]manifest.Option{
					"selection": {Type: manifest.TypeString, Default: "preset",
						Enum:        []string{"preset", "all-but-warning", "custom"},
						Description: "preset: what you ticked in the BleachBit window. all-but-warning: every cleaner without a warning. custom: the cleaners option"},
					"cleaners": {Type: manifest.TypeStrings, Default: []string{},
						Description: "For selection = custom: cleaner options such as thumbnails.cache (see bleachbit --list-cleaners)"},
					"max_items": {Type: manifest.TypeInteger, Default: 10, Min: manifest.Ptr(1),
						Description: "Show at most this many folders, largest first"},
				},
			}},
		},
		Checks: map[string]engine.CheckFunc{"preview": preview},
	}
}

// Options in BleachBit's selection that do more than free space.
var sensitive = map[string]string{
	"cookies": "cookies", "history": "history", "form_history": "form history",
	"passwords": "saved passwords", "session": "open sessions", "dom": "site storage",
	"site_preferences": "site settings", "sync": "sync data",
}

func preview(ctx context.Context, in engine.Input) (engine.Outcome, error) {
	args := []string{"--preview"}
	switch in.Options.String("selection") {
	case "preset":
		args = append(args, "--preset")
	case "all-but-warning":
		args = append(args, "--all-but-warning")
	case "custom":
		cl := in.Options.Strings("cleaners")
		if len(cl) == 0 {
			return engine.Outcome{}, errors.New("selection = \"custom\" needs a cleaners list")
		}
		args = append(args, cl...)
	}

	cmd := exec.CommandContext(ctx, "bleachbit", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return engine.Outcome{}, err
	}
	if err := cmd.Start(); err != nil {
		return engine.Outcome{}, err
	}
	sum := parsePreview(stdout)
	if err := cmd.Wait(); err != nil {
		if ctx.Err() != nil {
			return engine.Outcome{}, ctx.Err()
		}
		msg := strings.TrimSpace(lastLine(stderr.String()))
		if msg == "" {
			msg = err.Error()
		}
		return engine.Outcome{}, fmt.Errorf("bleachbit: %s", msg)
	}

	out := engine.Outcome{Hint: "clean with your selection: bleachbit --clean " + strings.Join(args[1:], " ")}
	if sum.files == 0 && sum.special == 0 {
		return out, nil
	}
	out.Summary = fmt.Sprintf("%s in %d files", sum.recover, sum.files)
	if sum.special > 0 {
		out.Summary += fmt.Sprintf(", plus %d database cleanups", sum.special)
	}

	groups := sum.sortedGroups()
	max := in.Options.Int("max_items")
	for i, g := range groups {
		if i == max {
			var rest int64
			for _, r := range groups[max:] {
				rest += r.size
			}
			out.Notes = append(out.Notes, fmt.Sprintf("and %d more folders (%s); raise max_items to see them", len(groups)-max, fsx.Human(rest)))
			break
		}
		detail := fmt.Sprintf("%d files", g.files)
		if g.special > 0 {
			detail += fmt.Sprintf(", %d databases cleaned", g.special)
		}
		conf := report.ConfidenceReview
		if strings.HasPrefix(g.path, filepath.Join(fsx.Home(), ".cache")+"/") || strings.HasPrefix(g.path, "/var/cache/") {
			conf = report.ConfidenceSafe
		}
		out.Items = append(out.Items, report.Item{
			ID: g.path, Label: fsx.Pretty(g.path), Path: g.path, Size: g.size,
			Detail: detail, Confidence: conf,
		})
	}
	if in.Options.String("selection") == "preset" {
		if s := sensitiveSelected(); s != "" {
			out.Notes = append(out.Notes, "your BleachBit selection also clears "+s+", which signs you out of websites")
		}
	}
	return out, nil
}

type group struct {
	path           string
	size           int64
	files, special int
}

type previewSummary struct {
	groups         map[string]*group
	files, special int
	recover        string // BleachBit's own total, e.g. "10.76GB"
}

func (s *previewSummary) sortedGroups() []*group {
	out := make([]*group, 0, len(s.groups))
	for _, g := range s.groups {
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].size != out[j].size {
			return out[i].size > out[j].size
		}
		return out[i].path < out[j].path
	})
	return out
}

// parsePreview reads `bleachbit --preview` output line by line:
//
//	Delete 16.4kB /home/me/.cache/thumbnails/normal/x.png
//	Clean file ?B /home/me/.config/google-chrome/Default/Preferences
//	Vacuum ?B /home/me/.config/google-chrome/Default/History
//	Disk space to be recovered: 10.76GB
//	Files to be deleted: 248580
//	Special operations: 44
func parsePreview(r io.Reader) *previewSummary {
	s := &previewSummary{groups: map[string]*group{}}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		var size, path string
		special := false
		switch {
		case strings.HasPrefix(line, "Delete "):
			size, path, _ = strings.Cut(strings.TrimPrefix(line, "Delete "), " ")
		case strings.HasPrefix(line, "Clean file "):
			size, path, _ = strings.Cut(strings.TrimPrefix(line, "Clean file "), " ")
			special = true
		case strings.HasPrefix(line, "Vacuum "):
			size, path, _ = strings.Cut(strings.TrimPrefix(line, "Vacuum "), " ")
			special = true
		case strings.HasPrefix(line, "Disk space to be recovered: "):
			s.recover = strings.TrimPrefix(line, "Disk space to be recovered: ")
			continue
		default:
			continue
		}
		if path == "" {
			continue
		}
		key := groupKey(path)
		g := s.groups[key]
		if g == nil {
			g = &group{path: key}
			s.groups[key] = g
		}
		if special {
			g.special++
			s.special++
		} else {
			g.files++
			s.files++
			g.size += fsx.ParseDecimalSize(size)
		}
	}
	if s.recover == "" {
		var total int64
		for _, g := range s.groups {
			total += g.size
		}
		s.recover = fsx.Human(total)
	}
	return s
}

// groupKey picks the folder a path is reported under: two levels below
// home (~/.cache/thumbnails, ~/.config/google-chrome) or the root
// (/var/cache), so one app's files land in one item.
func groupKey(path string) string {
	home := fsx.Home()
	base, rest := "/", strings.TrimPrefix(path, "/")
	if r, ok := strings.CutPrefix(path, home+"/"); ok {
		base, rest = home, r
	}
	parts := strings.Split(rest, "/")
	if len(parts) <= 1 {
		return path // a file directly in home or /
	}
	n := 2
	if len(parts)-1 < n {
		n = len(parts) - 1 // keep the file name out of the key
	}
	return filepath.Join(append([]string{base}, parts[:n]...)...)
}

// sensitiveSelected reads the GUI selection BleachBit saved and lists the
// enabled options that delete more than clutter.
func sensitiveSelected() string {
	cfgDir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(cfgDir, "bleachbit", "bleachbit.ini"))
	if err != nil {
		return ""
	}
	found := map[string]bool{}
	inTree := false
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			inTree = line == "[tree]"
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !inTree || !ok || !strings.EqualFold(strings.TrimSpace(v), "true") {
			continue
		}
		_, opt, ok := strings.Cut(strings.TrimSpace(k), ".")
		if desc, hit := sensitive[opt]; ok && hit {
			found[desc] = true
		}
	}
	var list []string
	for d := range found {
		list = append(list, d)
	}
	sort.Strings(list)
	return strings.Join(list, ", ")
}

func lastLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndex(s, "\n"); i >= 0 {
		return s[i+1:]
	}
	return s
}
