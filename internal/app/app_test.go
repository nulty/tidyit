package app

import (
	"testing"

	"tidyit/internal/config"
)

// The settings dialog is generated from the built-in and bundled manifests, so they
// must validate and carry what a form needs: a page and option labels.
func TestBuiltinManifestsAreComplete(t *testing.T) {
	// Only the bundled plugins, not whatever this machine has installed.
	t.Setenv("TIDYIT_PLUGIN_PATH", "../../plugins")
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_DATA_DIRS", t.TempDir())
	reg, loadWarnings := Registry()
	for _, w := range loadWarnings {
		t.Error(w)
	}
	if reg.Plugins()[len(reg.Plugins())-1].Manifest.ID != "home-usage" {
		t.Error("bundled home-usage plugin was not loaded")
	}
	for _, p := range reg.Plugins() {
		m := &p.Manifest
		if err := m.Validate(); err != nil {
			t.Errorf("plugin %s: %v", m.ID, err)
		}
		if m.Category == "" {
			t.Errorf("plugin %s: no category", m.ID)
		}
		for _, c := range m.Checks {
			for name, o := range c.Options {
				if o.Title == "" {
					t.Errorf("%s.%s: option %s has no title", m.ID, c.ID, name)
				}
			}
		}
	}
	insts, warnings := reg.Instances(&config.Config{Checks: map[string]*config.Entry{}})
	for _, w := range warnings {
		t.Error(w)
	}
	for _, in := range insts {
		if in.OptErr != nil || in.Defaults == nil {
			t.Errorf("%s: defaults do not resolve: %v", in.ID, in.OptErr)
		}
	}
}
