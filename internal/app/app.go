// Package app sets up a tidy run the same way for every front end: the
// plugin registry, config, check instances, and the order results are
// shown in. The CLI and the GUI both build on it.
package app

import (
	"fmt"
	"sort"

	"tidy/internal/checks/apt"
	"tidy/internal/checks/bleachbit"
	"tidy/internal/checks/core"
	"tidy/internal/checks/docker"
	"tidy/internal/checks/flatpak"
	"tidy/internal/checks/network"
	"tidy/internal/checks/snap"
	"tidy/internal/checks/systemd"
	"tidy/internal/config"
	"tidy/internal/engine"
	"tidy/internal/external"
	"tidy/pkg/report"
	"tidy/render/view"
)

// Headline lists the checks summarised in a report's header instead of
// being listed as rows.
var Headline = []string{"disk.usage"}

// Registry returns the built-in plugins followed by external ones, with
// warnings for external plugins that could not be loaded.
func Registry() (*engine.Registry, []string) {
	r := &engine.Registry{}
	for _, p := range core.Plugins() {
		r.Add(p)
	}
	r.Add(docker.Plugin())
	r.Add(snap.Plugin())
	r.Add(flatpak.Plugin())
	r.Add(apt.Plugin())
	r.Add(network.Plugin())
	r.Add(systemd.Plugin())
	r.Add(bleachbit.Plugin())

	builtin := map[string]bool{}
	for _, p := range r.Plugins() {
		builtin[p.Manifest.ID] = true
	}
	ext, warnings := external.LoadAll(external.Dirs())
	for _, p := range ext {
		if builtin[p.Manifest.ID] {
			warnings = append(warnings, fmt.Sprintf("plugin %s: id %q is a built-in plugin; skipping", p.Dir, p.Manifest.ID))
			continue
		}
		r.Add(p)
	}
	return r, warnings
}

// LoadConfig loads path, or the default config file if path is "" (a
// missing default file is not an error).
func LoadConfig(path string) (*config.Config, error) {
	if path != "" {
		return config.Load(path, true)
	}
	return config.Load(config.DefaultPath(), false)
}

// SortResults puts results back in instance order (they arrive in
// completion order).
func SortResults(rep *report.Report, insts []*engine.Instance) {
	pos := map[string]int{}
	for i, in := range insts {
		pos[in.ID] = i
	}
	sort.SliceStable(rep.Results, func(i, j int) bool { return pos[rep.Results[i].ID] < pos[rep.Results[j].ID] })
}

// Groups sections the results by plugin, in registry order.
func Groups(rep *report.Report, reg *engine.Registry) []view.Group {
	var out []view.Group
	for _, p := range reg.Plugins() {
		g := view.Group{Name: p.Manifest.Name}
		for _, r := range rep.Results {
			if r.Plugin == p.Manifest.ID {
				g.Results = append(g.Results, r)
			}
		}
		if len(g.Results) > 0 {
			out = append(out, g)
		}
	}
	return out
}

// Enabled counts the instances that will run.
func Enabled(insts []*engine.Instance) int {
	n := 0
	for _, in := range insts {
		if in.Enabled {
			n++
		}
	}
	return n
}
