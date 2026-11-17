package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"tidyit/internal/config"
	"tidyit/internal/engine"
	"tidyit/pkg/manifest"
)

// This file is the settings dialog's view model. Like model.go it has no
// GTK in it, so it is tested directly.

// settingsPage is one page of the Preferences dialog.
type settingsPage struct {
	Category manifest.Category
	Title    string
	Icon     string
	Groups   []pluginGroup
}

// pluginGroup is one plugin's checks on a settings page.
type pluginGroup struct {
	Title   string
	Missing []string // required tools not on PATH
	Insts   []*engine.Instance
}

// settingsPages lays the instances out on the Files, Packages and System
// pages, one group per plugin, in registry order.
func settingsPages(insts []*engine.Instance, lookPath func(string) (string, error)) []settingsPage {
	pages := []settingsPage{
		{Category: manifest.CategoryFiles, Title: "Files", Icon: "folder-symbolic"},
		{Category: manifest.CategoryPackages, Title: "Packages", Icon: "package-x-generic-symbolic"},
		{Category: manifest.CategorySystem, Title: "System", Icon: "emblem-system-symbolic"},
	}
	for _, in := range insts {
		m := &in.Plugin.Manifest
		for i := range pages {
			if pages[i].Category != m.Page() {
				continue
			}
			p := &pages[i]
			if n := len(p.Groups); n == 0 || p.Groups[n-1].Insts[0].Plugin != in.Plugin {
				g := pluginGroup{Title: m.Name}
				for _, bin := range m.Requires {
					if _, err := lookPath(bin); err != nil {
						g.Missing = append(g.Missing, bin)
					}
				}
				p.Groups = append(p.Groups, g)
			}
			g := &p.Groups[len(p.Groups)-1]
			g.Insts = append(g.Insts, in)
		}
	}
	return pages
}

// pageOf returns the name of the settings page that shows check id.
func pageOf(insts []*engine.Instance, id string) (manifest.Category, bool) {
	for _, in := range insts {
		if in.ID == id {
			return in.Plugin.Manifest.Page(), true
		}
	}
	return "", false
}

// missingText says which tools a plugin needs, e.g. "flatpak isn't
// installed, so these checks are skipped".
func missingText(missing []string) string {
	if len(missing) == 0 {
		return ""
	}
	verb := "isn't"
	if len(missing) > 1 {
		verb = "aren't"
	}
	return fmt.Sprintf("%s %s installed, so these checks are skipped", strings.Join(missing, " and "), verb)
}

// optionNames are a check's options in display order: what it looks at
// (folders, then other lists), then thresholds, choices and switches,
// each by title.
func optionNames(spec *manifest.CheckSpec) []string {
	names := make([]string, 0, len(spec.Options))
	for n := range spec.Options {
		names = append(names, n)
	}
	rank := func(o manifest.Option) int {
		switch {
		case o.Format == manifest.FormatPath:
			return 0
		case o.Type == manifest.TypeStrings:
			return 1
		case o.Type == manifest.TypeInteger || o.Type == manifest.TypeNumber:
			return 2
		case o.Type == manifest.TypeString:
			return 3
		}
		return 4
	}
	sort.Slice(names, func(i, j int) bool {
		oi, oj := spec.Options[names[i]], spec.Options[names[j]]
		if ri, rj := rank(oi), rank(oj); ri != rj {
			return ri < rj
		}
		a, b := optionLabel(oi, names[i]), optionLabel(oj, names[j])
		if a != b {
			return a < b
		}
		return names[i] < names[j]
	})
	return names
}

// optionLabel is an option's title, with its unit, e.g. "Minimum size
// (MB)". Without a title it falls back to the name, made readable.
func optionLabel(o manifest.Option, name string) string {
	label := o.Title
	if label == "" {
		label = sentence(strings.ReplaceAll(name, "_", " "))
	}
	if o.Unit != "" {
		label += " (" + o.Unit + ")"
	}
	return label
}

// isChanged reports whether an option differs from the check's default.
func isChanged(in *engine.Instance, name string) bool {
	return !sameValue(in.Options[name], in.Defaults[name])
}

// changedOptions lists the labels of the options that differ from their
// defaults, in display order.
func changedOptions(in *engine.Instance) []string {
	var out []string
	for _, n := range optionNames(in.Spec) {
		if isChanged(in, n) {
			out = append(out, optionLabel(in.Spec.Options[n], n))
		}
	}
	return out
}

// checkDescription is what a check does. Presets and user-defined checks
// share their template's description ("Find files or folders matching
// ..."), which says nothing about this one, so they have none.
func checkDescription(in *engine.Instance) string {
	if in.Origin != "default" {
		return ""
	}
	return in.Spec.Description
}

// canReset reports whether a check's options can go back to defaults. A
// check the user defined in config (use = "paths.scan") is its options:
// "resetting" its folders would leave it scanning nothing, so it can't.
func canReset(in *engine.Instance) bool { return in.Origin != "config" }

// numberDigits is how many decimals a number option shows: at least 2,
// and enough for the saved value, so showing it never rounds it (GTK
// re-reads a spin row's text and would save the rounded value).
func numberDigits(v float64) uint {
	s := strconv.FormatFloat(v, 'f', -1, 64)
	n := 0
	if i := strings.IndexByte(s, '.'); i >= 0 {
		n = len(s) - i - 1
	}
	return uint(min(max(n, 2), 6))
}

// checkSubtitle sums up a check's settings for its row: what was changed,
// else what the check does.
func checkSubtitle(in *engine.Instance) string {
	if !canReset(in) {
		return "Your own check, defined in config.toml"
	}
	switch ch := changedOptions(in); len(ch) {
	case 0:
		return checkDescription(in)
	case 1, 2:
		return "Changed: " + strings.Join(ch, ", ")
	default:
		return fmt.Sprintf("%d settings changed", len(ch))
	}
}

// override is what the config should hold for an option set to v: nil
// (no entry) when v is the default, so the file only keeps changes.
func override(in *engine.Instance, name string, v any) any {
	if sameValue(v, in.Defaults[name]) {
		return nil
	}
	return v
}

// enabledOverride is what the config should hold for a check switched to
// want: nil when that is what it would be anyway, from its default or a
// prefix entry such as [checks.docker].
func enabledOverride(cfg *config.Config, in *engine.Instance, want bool) *bool {
	base := in.DefaultEnabled
	if inh := cfg.Inherited(in.ID); inh != nil {
		base = *inh
	}
	if want == base {
		return nil
	}
	return &want
}

// validate checks a config entry's options against the check's schema,
// as the engine will when it next runs.
func validate(cfg *config.Config, in *engine.Instance) error {
	var opts map[string]any
	if e := cfg.Checks[in.ID]; e != nil {
		opts = e.Options
	}
	_, err := in.Spec.Resolve(map[string]any(in.Defaults), opts)
	return err
}

// sameValue compares option values, treating a nil list as empty.
func sameValue(a, b any) bool {
	if as, ok := a.([]string); ok {
		bs, _ := b.([]string)
		return len(as) == 0 && len(bs) == 0 || reflect.DeepEqual(as, bs)
	}
	return reflect.DeepEqual(a, b)
}

// tildePath shows a path under home as ~/..., the way the presets write
// them, so a picked folder reads like the built-in ones.
func tildePath(path, home string) string {
	if home == "" || home == "/" {
		return path
	}
	if path == home {
		return "~"
	}
	if rel, ok := strings.CutPrefix(path, home+string(filepath.Separator)); ok {
		return "~/" + rel
	}
	return path
}

// addValue appends v to a list option unless it is empty or already there.
func addValue(list []string, v string) ([]string, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return list, false
	}
	for _, e := range list {
		if e == v {
			return list, false
		}
	}
	return append(append([]string{}, list...), v), true
}

// removeValue returns list without its i'th entry.
func removeValue(list []string, i int) []string {
	out := append([]string{}, list[:i]...)
	return append(out, list[i+1:]...)
}

var homeDir, _ = os.UserHomeDir()

// lookPath finds required tools; tests replace it.
var lookPath = exec.LookPath
