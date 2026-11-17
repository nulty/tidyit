package main

import (
	"errors"
	"reflect"
	"testing"

	"tidyit/internal/config"
	"tidyit/internal/engine"
	"tidyit/pkg/manifest"
)

func plugin(id, name string, cat manifest.Category, requires ...string) *engine.Plugin {
	return &engine.Plugin{Manifest: manifest.Manifest{ID: id, Name: name, Category: cat, Requires: requires}}
}

func inst(id string, p *engine.Plugin) *engine.Instance {
	spec := &manifest.CheckSpec{ID: id, Options: map[string]manifest.Option{
		"min_size_mb": {Type: manifest.TypeInteger, Title: "Minimum size", Unit: "MB"},
		"paths":       {Type: manifest.TypeStrings, Title: "Folders"},
		"days":        {Type: manifest.TypeInteger},
	}}
	defaults := manifest.Values{"min_size_mb": 200, "paths": []string{"~/.cache"}, "days": 0}
	opts := manifest.Values{}
	for k, v := range defaults {
		opts[k] = v
	}
	return &engine.Instance{ID: id, Title: id, Plugin: p, Spec: spec, Options: opts, Defaults: defaults,
		Enabled: true, DefaultEnabled: true}
}

func TestSettingsPagesGroupByPlugin(t *testing.T) {
	files := plugin("paths", "Files", manifest.CategoryFiles)
	docker := plugin("docker", "Docker", manifest.CategoryPackages, "docker")
	ext := plugin("ext", "Extra", "")
	insts := []*engine.Instance{inst("downloads", files), inst("user-cache", files), inst("docker.images", docker), inst("ext.x", ext)}
	noDocker := func(bin string) (string, error) {
		if bin == "docker" {
			return "", errors.New("not found")
		}
		return "/usr/bin/" + bin, nil
	}
	var got []string
	for _, p := range settingsPages(insts, noDocker) {
		for _, g := range p.Groups {
			line := string(p.Category) + "/" + g.Title + ":"
			for _, in := range g.Insts {
				line += " " + in.ID
			}
			if g.Missing != nil {
				line += " missing " + missingText(g.Missing)
			}
			got = append(got, line)
		}
	}
	want := []string{
		"files/Files: downloads user-cache",
		"packages/Docker: docker.images missing docker isn't installed, so these checks are skipped",
		"system/Extra: ext.x",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got\n%q\nwant\n%q", got, want)
	}
}

func TestOptionLabelsAndOrder(t *testing.T) {
	in := inst("c", nil)
	in.Spec.Options["paths"] = manifest.Option{Type: manifest.TypeStrings, Title: "Folders", Format: manifest.FormatPath}
	in.Spec.Options["skip"] = manifest.Option{Type: manifest.TypeStrings, Title: "Skip"}
	in.Spec.Options["mine"] = manifest.Option{Type: manifest.TypeBoolean, Title: "Mine"}
	in.Spec.Options["mode"] = manifest.Option{Type: manifest.TypeString, Title: "Mode"}
	want := []string{"paths", "skip", "days", "min_size_mb", "mode", "mine"}
	if got := optionNames(in.Spec); !reflect.DeepEqual(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
	if got := optionLabel(in.Spec.Options["min_size_mb"], "min_size_mb"); got != "Minimum size (MB)" {
		t.Errorf("label = %q", got)
	}
	if got := optionLabel(manifest.Option{}, "owned_by_me"); got != "Owned by me" {
		t.Errorf("fallback label = %q", got)
	}
}

func TestChangesAndOverrides(t *testing.T) {
	in := inst("c", nil)
	if checkSubtitle(in) != "" || override(in, "min_size_mb", 200) != nil {
		t.Fatal("defaults should count as unchanged and store nothing")
	}
	if override(in, "paths", []string{"~/.cache"}) != nil {
		t.Error("an equal list should store nothing")
	}
	in.Options["min_size_mb"] = 500
	if got := checkSubtitle(in); got != "Changed: Minimum size (MB)" {
		t.Errorf("subtitle = %q", got)
	}
	in.Options["paths"] = []string{}
	in.Options["days"] = 3
	if got := checkSubtitle(in); got != "3 settings changed" {
		t.Errorf("subtitle = %q", got)
	}
	if !sameValue([]string(nil), []string{}) {
		t.Error("nil and empty lists should match")
	}
}

func TestEnabledOverrideRespectsPrefixEntries(t *testing.T) {
	in := inst("docker.images", nil)
	off := false
	cfg := &config.Config{Checks: map[string]*config.Entry{}}
	if enabledOverride(cfg, in, true) != nil {
		t.Error("on by default: switching on should store nothing")
	}
	if v := enabledOverride(cfg, in, false); v == nil || *v {
		t.Error("switching off should store false")
	}
	cfg.Checks["docker"] = &config.Entry{Enabled: &off}
	if v := enabledOverride(cfg, in, true); v == nil || !*v {
		t.Error("with [checks.docker] off, switching one check on must store true")
	}
}

func TestValidateUsesDefaultsAndEntry(t *testing.T) {
	in := inst("c", nil)
	in.Spec.Options["min_size_mb"] = manifest.Option{Type: manifest.TypeInteger, Min: manifest.Ptr(0)}
	cfg := &config.Config{Checks: map[string]*config.Entry{}}
	if err := validate(cfg, in); err != nil {
		t.Fatal(err)
	}
	cfg.SetOption("c", "min_size_mb", -1)
	if validate(cfg, in) == nil {
		t.Error("a value below min should fail")
	}
}

func TestListHelpers(t *testing.T) {
	list := []string{"a"}
	if got, ok := addValue(list, " b "); !ok || !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("add = %v %v", got, ok)
	}
	if _, ok := addValue(list, "a"); ok {
		t.Error("duplicates should not be added")
	}
	if _, ok := addValue(list, "  "); ok {
		t.Error("blank should not be added")
	}
	if got := removeValue([]string{"a", "b", "c"}, 1); !reflect.DeepEqual(got, []string{"a", "c"}) {
		t.Errorf("remove = %v", got)
	}
	for in, want := range map[string]string{"/home/me": "~", "/home/me/Downloads": "~/Downloads", "/home/meow": "/home/meow", "/tmp": "/tmp"} {
		if got := tildePath(in, "/home/me"); got != want {
			t.Errorf("tildePath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPresetsHideTemplateDescription(t *testing.T) {
	in := inst("downloads", nil)
	in.Spec.Description = "Find files or folders matching patterns"
	in.Origin = "preset"
	if got := checkSubtitle(in); got != "" {
		t.Errorf("preset subtitle = %q, want none", got)
	}
	in.Origin = "default"
	if got := checkSubtitle(in); got != in.Spec.Description {
		t.Errorf("default check subtitle = %q", got)
	}
}
