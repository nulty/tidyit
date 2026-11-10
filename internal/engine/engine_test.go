package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"tidyit/internal/config"
	"tidyit/pkg/manifest"
	"tidyit/pkg/report"
)

func testPlugin() *Plugin {
	return &Plugin{
		Manifest: manifest.Manifest{ID: "t", Name: "Test", Checks: []manifest.CheckSpec{
			{ID: "a", Title: "A", Options: map[string]manifest.Option{
				"n": {Type: manifest.TypeInteger, Default: 1}}},
			{ID: "b", Title: "B"},
			{ID: "tmpl", Title: "Template", Template: true, Options: map[string]manifest.Option{
				"path": {Type: manifest.TypeString}}},
		}},
		Checks: map[string]CheckFunc{
			"a": func(_ context.Context, in Input) (Outcome, error) {
				if in.Options.Int("n") > 1 {
					return Outcome{Items: []report.Item{{ID: "x"}}}, nil
				}
				return Outcome{}, nil
			},
			"b":    func(context.Context, Input) (Outcome, error) { return Outcome{}, nil },
			"tmpl": func(context.Context, Input) (Outcome, error) { return Outcome{}, nil },
		},
		Presets: []Preset{{ID: "preset-one", Check: "tmpl", Title: "Preset", Options: map[string]any{"path": "/p"}}},
	}
}

func loadTOML(t *testing.T, body string) *config.Config {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(p, true)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func byID(insts []*Instance) map[string]*Instance {
	m := map[string]*Instance{}
	for _, in := range insts {
		m[in.ID] = in
	}
	return m
}

func TestInstancesFromDefaultsPresetsAndConfig(t *testing.T) {
	r := &Registry{}
	r.Add(testPlugin())
	cfg := loadTOML(t, `
[checks.t]
enabled = false

[checks.t.a]
enabled = true
n = 5

[checks.mine]
use = "t.tmpl"
title = "Mine"
path = "/mine"

[checks.nope]
x = 1
`)
	insts, warnings := r.Instances(cfg)
	got := byID(insts)
	if strings.Join(SortedIDs(insts), ",") != "mine,preset-one,t.a,t.b" {
		t.Fatalf("ids = %v", SortedIDs(insts))
	}
	if !got["t.a"].Enabled || got["t.a"].Options.Int("n") != 5 {
		t.Errorf("t.a: specific entry should win over [checks.t]: %+v", got["t.a"])
	}
	if got["t.b"].Enabled {
		t.Errorf("t.b should be disabled by [checks.t]")
	}
	if got["preset-one"].Options.String("path") != "/p" {
		t.Errorf("preset options not applied")
	}
	if got["mine"].Title != "Mine" || got["mine"].Options.String("path") != "/mine" {
		t.Errorf("user instance wrong: %+v", got["mine"])
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "nope") {
		t.Errorf("warnings = %v", warnings)
	}
}

func TestBadOptionBecomesErrorResult(t *testing.T) {
	r := &Registry{}
	r.Add(testPlugin())
	insts, _ := r.Instances(loadTOML(t, "[checks.t.a]\nn = \"many\"\n"))
	rep := Collect(Runner{}.Run(context.Background(), insts), "h", nil)
	for _, res := range rep.Results {
		if res.ID == "t.a" && (res.Status != report.StatusError || !strings.Contains(res.Error, "config:")) {
			t.Fatalf("t.a = %+v", res)
		}
	}
}

func run(t *testing.T, p *Plugin, r Runner) map[string]report.Result {
	t.Helper()
	reg := &Registry{}
	reg.Add(p)
	insts, _ := reg.Instances(&config.Config{Checks: map[string]*config.Entry{}})
	out := map[string]report.Result{}
	for _, res := range Collect(r.Run(context.Background(), insts), "h", nil).Results {
		out[res.ID] = res
	}
	return out
}

func TestRunnerStatuses(t *testing.T) {
	p := &Plugin{
		Manifest: manifest.Manifest{ID: "r", Checks: []manifest.CheckSpec{
			{ID: "slow"}, {ID: "skip"}, {ID: "boom"}, {ID: "flag", Hint: "do it"}, {ID: "err"},
		}},
		Checks: map[string]CheckFunc{
			"slow": func(ctx context.Context, _ Input) (Outcome, error) {
				<-ctx.Done()
				return Outcome{}, ctx.Err()
			},
			"skip": func(context.Context, Input) (Outcome, error) { return Outcome{}, Skip("not here") },
			"boom": func(context.Context, Input) (Outcome, error) { panic("kaboom") },
			"flag": func(context.Context, Input) (Outcome, error) {
				return Outcome{Items: []report.Item{{ID: "1"}}}, nil
			},
			"err": func(context.Context, Input) (Outcome, error) { return Outcome{}, os.ErrPermission },
		},
	}
	got := run(t, p, Runner{Timeout: 50 * time.Millisecond})
	check := func(id string, st report.Status, field, want string) {
		t.Helper()
		r := got["r."+id]
		if r.Status != st || !strings.Contains(field, want) {
			t.Errorf("%s: status %s (want %s), %q does not contain %q", id, r.Status, st, field, want)
		}
	}
	check("slow", report.StatusError, got["r.slow"].Error, "timed out")
	check("skip", report.StatusSkipped, got["r.skip"].SkipReason, "not here")
	check("boom", report.StatusError, got["r.boom"].Error, "kaboom")
	check("flag", report.StatusFlag, got["r.flag"].Hint, "do it")
	check("err", report.StatusError, got["r.err"].Error, "permission")
}

func TestMissingBinarySkipsWholePlugin(t *testing.T) {
	var calls atomic.Int32
	p := &Plugin{
		Manifest: manifest.Manifest{ID: "m", Requires: []string{"definitely-not-a-real-binary-tidyit"},
			Checks: []manifest.CheckSpec{{ID: "a"}, {ID: "b"}}},
		Checks: map[string]CheckFunc{
			"a": func(context.Context, Input) (Outcome, error) { calls.Add(1); return Outcome{}, nil },
			"b": func(context.Context, Input) (Outcome, error) { calls.Add(1); return Outcome{}, nil },
		},
	}
	for id, r := range run(t, p, Runner{}) {
		if r.Status != report.StatusSkipped || !strings.Contains(r.SkipReason, "not installed") {
			t.Errorf("%s: %+v", id, r)
		}
	}
	if calls.Load() != 0 {
		t.Errorf("checks ran %d times despite missing binary", calls.Load())
	}
}

func TestLockSerialisesPlugin(t *testing.T) {
	var running, maxRunning atomic.Int32
	fn := func(context.Context, Input) (Outcome, error) {
		n := running.Add(1)
		for {
			m := maxRunning.Load()
			if n <= m || maxRunning.CompareAndSwap(m, n) {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
		running.Add(-1)
		return Outcome{}, nil
	}
	p := &Plugin{
		Manifest: manifest.Manifest{ID: "l", Lock: "dpkg", Checks: []manifest.CheckSpec{{ID: "a"}, {ID: "b"}, {ID: "c"}}},
		Checks:   map[string]CheckFunc{"a": fn, "b": fn, "c": fn},
	}
	run(t, p, Runner{})
	if maxRunning.Load() != 1 {
		t.Fatalf("locked checks overlapped: max %d at once", maxRunning.Load())
	}
}

func TestDefaultOffNeedsConfigOrCheckFlag(t *testing.T) {
	newReg := func() *Registry {
		p := testPlugin()
		p.Manifest.Checks[1].DefaultOff = true // t.b
		r := &Registry{}
		r.Add(p)
		return r
	}
	insts, _ := newReg().Instances(&config.Config{Checks: map[string]*config.Entry{}})
	if byID(insts)["t.b"].Enabled {
		t.Fatal("t.b should be off by default")
	}
	insts, _ = newReg().Instances(loadTOML(t, "[checks.t.b]\nenabled = true\n"))
	if !byID(insts)["t.b"].Enabled {
		t.Fatal("config should turn t.b on")
	}
	insts, _ = newReg().Instances(&config.Config{Checks: map[string]*config.Entry{}})
	kept, _ := Filter(insts, []string{"t.b"})
	if len(kept) != 1 || !kept[0].Enabled {
		t.Fatal("--check t.b should run it")
	}
}
