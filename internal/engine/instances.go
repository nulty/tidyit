package engine

import (
	"fmt"
	"path"
	"sort"
	"strings"
	"time"

	"tidy/internal/config"
	"tidy/pkg/manifest"
)

// Instance is one runnable check: a plugin's check with resolved options.
type Instance struct {
	ID      string
	Title   string
	Hint    string
	Plugin  *Plugin
	Spec    *manifest.CheckSpec
	Options manifest.Values
	OptErr  error // set when the options failed validation
	Enabled bool
	Timeout time.Duration // 0 = runner default
	Origin  string        // "default", "preset" or "config"
}

// Registry holds the plugins tidy knows about, in display order.
type Registry struct {
	plugins []*Plugin
}

// Add registers a plugin. Plugins are shown in the order they are added.
func (r *Registry) Add(p *Plugin) { r.plugins = append(r.plugins, p) }

// Plugins returns the registered plugins.
func (r *Registry) Plugins() []*Plugin { return r.plugins }

func (r *Registry) find(pluginID string) *Plugin {
	for _, p := range r.plugins {
		if p.Manifest.ID == pluginID {
			return p
		}
	}
	return nil
}

// Instances builds every check instance from the plugins' defaults and
// presets, then applies the user's config. It returns warnings for config
// entries that match nothing.
func (r *Registry) Instances(cfg *config.Config) ([]*Instance, []string) {
	var (
		out      []*Instance
		byID     = map[string]*Instance{}
		warnings []string
	)
	add := func(in *Instance) {
		if prev, dup := byID[in.ID]; dup {
			warnings = append(warnings, fmt.Sprintf("check id %q is defined twice (%s and %s); keeping the first", in.ID, prev.Origin, in.Origin))
			return
		}
		byID[in.ID] = in
		out = append(out, in)
	}

	presetOpts := map[string]map[string]any{}
	for _, p := range r.plugins {
		for i := range p.Manifest.Checks {
			spec := &p.Manifest.Checks[i]
			if spec.Template {
				continue
			}
			add(&Instance{ID: p.Manifest.ID + "." + spec.ID, Title: spec.Title, Hint: spec.Hint,
				Plugin: p, Spec: spec, Enabled: true, Origin: "default"})
		}
		for _, ps := range p.Presets {
			spec, ok := p.Manifest.Check(ps.Check)
			if !ok {
				warnings = append(warnings, fmt.Sprintf("preset %q refers to unknown check %s.%s", ps.ID, p.Manifest.ID, ps.Check))
				continue
			}
			hint := ps.Hint
			if hint == "" {
				hint = spec.Hint
			}
			add(&Instance{ID: ps.ID, Title: ps.Title, Hint: hint, Plugin: p, Spec: spec, Enabled: true, Origin: "preset"})
			presetOpts[ps.ID] = ps.Options
		}
	}

	// User-defined instances: entries with `use = "plugin.check"`.
	for _, id := range cfg.IDs() {
		e := cfg.Checks[id]
		if e.Use == "" {
			continue
		}
		pluginID, checkID, ok := strings.Cut(e.Use, ".")
		p := r.find(pluginID)
		if !ok || p == nil {
			warnings = append(warnings, fmt.Sprintf("check %q: use = %q does not name a known plugin check", id, e.Use))
			continue
		}
		spec, ok := p.Manifest.Check(checkID)
		if !ok {
			warnings = append(warnings, fmt.Sprintf("check %q: plugin %q has no check %q", id, pluginID, checkID))
			continue
		}
		title := e.Title
		if title == "" {
			title = id
		}
		add(&Instance{ID: id, Title: title, Hint: spec.Hint, Plugin: p, Spec: spec, Enabled: true, Origin: "config"})
	}

	// Apply config to every instance: enabled flags (including on prefixes
	// such as [checks.docker]), titles, timeouts and options.
	matched := map[string]bool{}
	for _, in := range out {
		var cfgOpts map[string]any
		for _, id := range cfg.IDs() {
			e := cfg.Checks[id]
			switch {
			case id == in.ID:
				matched[id] = true
				cfgOpts = e.Options
				if e.Title != "" {
					in.Title = e.Title
				}
				if e.Timeout > 0 {
					in.Timeout = e.Timeout
				}
				if e.Enabled != nil {
					in.Enabled = *e.Enabled
				}
			case strings.HasPrefix(in.ID, id+".") && e.Enabled != nil:
				matched[id] = true
				// A more specific entry (applied in the id == in.ID case)
				// wins because IDs() sorts shorter prefixes first.
				in.Enabled = *e.Enabled
			}
		}
		in.Options, in.OptErr = in.Spec.Resolve(presetOpts[in.ID], cfgOpts)
	}
	for _, id := range cfg.IDs() {
		if !matched[id] && cfg.Checks[id].Use == "" {
			warnings = append(warnings, fmt.Sprintf("config [checks.%s] matches no check (see `tidy checks`)", id))
		}
	}
	return out, warnings
}

// Filter keeps instances whose id matches any of the glob patterns
// (path.Match syntax, e.g. "docker.*"). No patterns keeps everything.
func Filter(insts []*Instance, patterns []string) ([]*Instance, error) {
	if len(patterns) == 0 {
		return insts, nil
	}
	var out []*Instance
	for _, in := range insts {
		for _, pat := range patterns {
			ok, err := path.Match(pat, in.ID)
			if err != nil {
				return nil, fmt.Errorf("bad pattern %q: %w", pat, err)
			}
			// Also match on the plugin id, so --check docker works.
			if ok || pat == in.Plugin.Manifest.ID {
				out = append(out, in)
				break
			}
		}
	}
	return out, nil
}

// SortedIDs is a test and listing helper.
func SortedIDs(insts []*Instance) []string {
	ids := make([]string, len(insts))
	for i, in := range insts {
		ids[i] = in.ID
	}
	sort.Strings(ids)
	return ids
}
