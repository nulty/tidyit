// Package flatpak is a bundled plugin. It runs when flatpak is installed.
//
// flatpak has no dry run for `uninstall --unused` (and --noninteractive
// implies --assumeyes), so tidy never runs it. It works out unused runtimes
// itself from `flatpak list` and app metadata, erring towards not flagging.
package flatpak

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"sort"
	"strings"

	"tidy/internal/engine"
	"tidy/internal/execx"
	"tidy/pkg/manifest"
	"tidy/pkg/report"
)

// Plugin returns the flatpak plugin.
func Plugin() *engine.Plugin {
	return &engine.Plugin{
		Manifest: manifest.Manifest{
			ID: "flatpak", Name: "Flatpak", Version: "0.1.0", Protocol: manifest.ProtocolVersion,
			Requires: []string{"flatpak"},
			Checks: []manifest.CheckSpec{{
				ID:    "unused-runtimes",
				Title: "Unused Flatpak runtimes",
				Hint:  "flatpak uninstall --unused shows its own list and asks before removing (pinned runtimes are kept)",
			}},
		},
		Checks: map[string]engine.CheckFunc{"unused-runtimes": unusedRuntimes},
	}
}

// ref is a parsed "id/arch/branch" flatpak ref.
type ref struct{ id, arch, branch string }

func parseRef(s string) (ref, bool) {
	s = strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(s), "runtime/"), "app/")
	p := strings.Split(s, "/")
	if len(p) != 3 || p[0] == "" {
		return ref{}, false
	}
	return ref{p[0], p[1], p[2]}, true
}

func (r ref) String() string { return r.id + "/" + r.arch + "/" + r.branch }

// app is an installed application and the runtimes it names.
type app struct {
	ref      string
	runtime  ref
	sdk      ref
	sdkKnown bool
}

// parseApps reads `flatpak list --app --columns=ref,runtime` (tab separated).
func parseApps(out []byte) []app {
	var apps []app
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		f := strings.Split(sc.Text(), "\t")
		if len(f) < 2 {
			continue
		}
		rt, ok := parseRef(f[1])
		if !ok {
			continue
		}
		apps = append(apps, app{ref: strings.TrimSpace(f[0]), runtime: rt})
	}
	return apps
}

// parseRefs reads one ref per line, e.g. `flatpak list --runtime --columns=ref`.
func parseRefs(out []byte) []ref {
	var refs []ref
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		if r, ok := parseRef(sc.Text()); ok {
			refs = append(refs, r)
		}
	}
	return refs
}

// metadataSDK reads sdk= from the [Application] group of app metadata.
func metadataSDK(meta []byte) (ref, bool) {
	group := ""
	sc := bufio.NewScanner(bytes.NewReader(meta))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "[") {
			group = line
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok && group == "[Application]" && strings.TrimSpace(k) == "sdk" {
			return parseRef(v)
		}
	}
	return ref{}, false
}

// unused returns the runtimes that no app uses. A runtime counts as used
// when an app names it as runtime or sdk, or when its id extends a used id
// (org.gnome.Platform.Locale, org.freedesktop.Platform.GL.default...), at
// any branch, since extension branches need not match their parent's.
// If some app's sdk is unknown, no runtime ending in .Sdk is flagged.
func unused(runtimes []ref, apps []app) []ref {
	used := map[string]bool{}   // full ref
	usedID := map[string]bool{} // id only
	sdkUnknown := false
	for _, a := range apps {
		used[a.runtime.String()] = true
		usedID[a.runtime.id] = true
		if !a.sdkKnown {
			sdkUnknown = true
		} else if a.sdk.id != "" {
			used[a.sdk.String()] = true
			usedID[a.sdk.id] = true
		}
	}
	var out []ref
	for _, r := range runtimes {
		if used[r.String()] || extendsUsed(r.id, usedID) {
			continue
		}
		if sdkUnknown && (strings.HasSuffix(r.id, ".Sdk") || strings.Contains(r.id, ".Sdk.")) {
			continue
		}
		out = append(out, r)
	}
	return out
}

func extendsUsed(id string, usedID map[string]bool) bool {
	for i := strings.LastIndex(id, "."); i > 0; i = strings.LastIndex(id[:i], ".") {
		if usedID[id[:i]] {
			return true
		}
	}
	return false
}

func unusedRuntimes(ctx context.Context, _ engine.Input) (engine.Outcome, error) {
	rawApps, err := execx.Output(ctx, "flatpak", "list", "--app", "--columns=ref,runtime")
	if err != nil {
		return engine.Outcome{}, err
	}
	rawRT, err := execx.Output(ctx, "flatpak", "list", "--runtime", "--columns=ref")
	if err != nil {
		return engine.Outcome{}, err
	}
	apps := parseApps(rawApps)
	var out engine.Outcome
	for i := range apps {
		meta, err := execx.Output(ctx, "flatpak", "info", "--show-metadata", apps[i].ref)
		if err != nil {
			if ctx.Err() != nil {
				return engine.Outcome{}, ctx.Err()
			}
			out.Notes = append(out.Notes, "could not read metadata for "+apps[i].ref+", so SDKs are not flagged")
			continue
		}
		apps[i].sdk, _ = metadataSDK(meta)
		apps[i].sdkKnown = true
	}
	refs := unused(parseRefs(rawRT), apps)
	sort.Slice(refs, func(i, j int) bool { return refs[i].String() < refs[j].String() })
	for _, r := range refs {
		out.Items = append(out.Items, report.Item{
			ID: r.String(), Label: r.id, Detail: r.branch, Confidence: report.ConfidenceReview,
			Action: &report.Action{Command: "flatpak uninstall " + r.String()},
		})
	}
	if n := len(out.Items); n > 0 {
		out.Summary = fmt.Sprintf("tidy's estimate of %d; compare with flatpak's own list before removing", n)
	}
	return out, nil
}
