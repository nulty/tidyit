package external

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tidyit/internal/engine"
	"tidyit/pkg/report"
)

const okManifest = `
id = "demo"
name = "Demo"
version = "0.1.0"
protocol = 1

[[checks]]
id = "things"
title = "Things"
hint = "remove with rm"

[checks.options.count]
type = "integer"
default = 2
min = 1
description = "how many"
`

// writePlugin makes a plugin folder with the given manifest and run script.
func writePlugin(t *testing.T, manifestText, script string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "demo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ManifestFile), []byte(manifestText), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "run"), []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func runCheck(t *testing.T, p *engine.Plugin, opts map[string]any) *report.Result {
	t.Helper()
	spec, _ := p.Manifest.Check("things")
	vals, err := spec.Resolve(opts)
	if err != nil {
		t.Fatal(err)
	}
	inst := &engine.Instance{ID: "demo.things", Title: spec.Title, Hint: spec.Hint, Plugin: p, Spec: spec, Options: vals, Enabled: true}
	r := engine.Runner{Timeout: 5 * time.Second}
	rep := engine.Collect(r.Run(context.Background(), []*engine.Instance{inst}), "h", nil)
	if len(rep.Results) != 1 {
		t.Fatalf("got %d results", len(rep.Results))
	}
	return &rep.Results[0]
}

func TestRunItemsNotesSummary(t *testing.T) {
	// The script echoes back the request so the test can check it, and
	// emits one of each message type.
	dir := writePlugin(t, okManifest, `
req=$(cat)
case "$req" in *'"count":3'*) ;; *) echo "bad request: $req" >&2; exit 1;; esac
case "$req" in *'"check":"things"'*'"instance":"demo.things"'*) ;; *) echo "bad ids: $req" >&2; exit 1;; esac
[ "$TIDYIT_PROTOCOL" = 1 ] || exit 3
[ "$(pwd)" = "$TIDYIT_PLUGIN_DIR" ] || exit 4
echo '{"type":"item","id":"a","label":"Thing A","size":2048,"confidence":"safe","action":{"command":"rm a"}}'
echo
echo '{"type":"item","label":"Thing B","detail":"old"}'
echo '{"type":"note","text":"looked in 2 places"}'
echo '{"type":"summary","text":"2 things","hint":"custom hint"}'
echo 'some debugging' >&2
`)
	p, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	res := runCheck(t, p, map[string]any{"count": 3})
	if res.Status != report.StatusFlag {
		t.Fatalf("status %s, error %q, debug %q", res.Status, res.Error, res.Debug)
	}
	if len(res.Items) != 2 || res.Items[0].Size != 2048 || res.Items[0].Action.Command != "rm a" {
		t.Fatalf("items %+v", res.Items)
	}
	if b := res.Items[1]; b.ID != "Thing B" || b.Confidence != report.ConfidenceReview {
		t.Fatalf("item B defaults: %+v", b)
	}
	if res.Summary != "2 things" || res.Hint != "custom hint" || len(res.Notes) != 1 {
		t.Fatalf("summary %q hint %q notes %v", res.Summary, res.Hint, res.Notes)
	}
	if !strings.Contains(res.Debug, "some debugging") {
		t.Fatalf("debug %q", res.Debug)
	}
}

func TestRunOutcomes(t *testing.T) {
	cases := []struct {
		name, script string
		status       report.Status
		want         string // substring of error or skip reason
	}{
		{"ok", `cat >/dev/null; echo '{"type":"note","text":"all fine"}'`, report.StatusOK, ""},
		{"skip", `echo '{"type":"skip","reason":"nothing to look at"}'`, report.StatusSkipped, "nothing to look at"},
		{"exit", `echo 'tool exploded' >&2; exit 2`, report.StatusError, "tool exploded"},
		{"garbage", `echo 'hello'`, report.StatusError, "line 1"},
		{"unknown type", `echo '{"type":"result"}'`, report.StatusError, "unknown message type"},
		{"unknown field", `echo '{"type":"item","id":"x","colour":"red"}'`, report.StatusError, "colour"},
		{"bad confidence", `echo '{"type":"item","id":"x","confidence":"yolo"}'`, report.StatusError, "confidence"},
		{"duplicate id", `echo '{"type":"item","id":"x"}'; echo '{"type":"item","id":"x"}'`, report.StatusError, "twice"},
		{"skip with items", `echo '{"type":"item","id":"x"}'; echo '{"type":"skip","reason":"r"}'`, report.StatusError, "skip together"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, err := Load(writePlugin(t, okManifest, c.script))
			if err != nil {
				t.Fatal(err)
			}
			res := runCheck(t, p, nil)
			if res.Status != c.status {
				t.Fatalf("status %s, want %s (error %q)", res.Status, c.status, res.Error)
			}
			if got := res.Error + res.SkipReason; !strings.Contains(got, c.want) {
				t.Fatalf("got %q, want it to contain %q", got, c.want)
			}
		})
	}
}

func TestTimeoutKillsChildren(t *testing.T) {
	// The child sleep keeps stdout open; the process group kill must end it.
	m := strings.Replace(okManifest, `hint = "remove with rm"`, `timeout = "300ms"`, 1)
	p, err := Load(writePlugin(t, m, `sleep 30 & wait`))
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	res := runCheck(t, p, nil)
	if res.Status != report.StatusError || !strings.Contains(res.Error, "timed out") {
		t.Fatalf("status %s error %q", res.Status, res.Error)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("took %s to time out", d)
	}
}

func TestLoadRejects(t *testing.T) {
	cases := map[string]string{
		"unknown key":   okManifest + "\ncolour = \"red\"\n",
		"protocol":      strings.Replace(okManifest, "protocol = 1", "protocol = 2", 1),
		"bad id":        strings.Replace(okManifest, `id = "demo"`, `id = "Demo Plugin"`, 1),
		"bad default":   strings.Replace(okManifest, "default = 2", "default = 0", 1),
		"no checks":     "id = \"demo\"\nprotocol = 1\n",
		"bad type":      strings.Replace(okManifest, `type = "integer"`, `type = "date"`, 1),
		"escaping exec": okManifest[:strings.Index(okManifest, "[[checks]]")] + "entrypoint = \"../../bin/sh\"\n" + okManifest[strings.Index(okManifest, "[[checks]]"):],
	}
	for name, m := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Load(writePlugin(t, m, "exit 0"))
			if err == nil {
				t.Fatal("loaded")
			}
			t.Log(err)
		})
	}
	t.Run("not executable", func(t *testing.T) {
		dir := writePlugin(t, okManifest, "exit 0")
		if err := os.Chmod(filepath.Join(dir, "run"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(dir); err == nil || !strings.Contains(err.Error(), "executable") {
			t.Fatalf("err %v", err)
		}
	})
}

func TestLoadAll(t *testing.T) {
	root := t.TempDir()
	good := writePlugin(t, okManifest, "exit 0")
	if err := os.Rename(good, filepath.Join(root, "good")); err != nil {
		t.Fatal(err)
	}
	bad := writePlugin(t, "id = \"x\"\nprotocol = 1\n", "exit 0")
	if err := os.Rename(bad, filepath.Join(root, "bad")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "not-a-plugin"), 0o755); err != nil {
		t.Fatal(err)
	}
	plugins, warnings := LoadAll([]string{root, filepath.Join(root, "missing")})
	if len(plugins) != 1 || plugins[0].Manifest.ID != "demo" {
		t.Fatalf("plugins %v", plugins)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "bad") {
		t.Fatalf("warnings %q", warnings)
	}
}

func TestNeedsRootSkips(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root")
	}
	p, err := Load(writePlugin(t, strings.Replace(okManifest, "protocol = 1", "protocol = 1\nneeds_root = true", 1), "exit 1"))
	if err != nil {
		t.Fatal(err)
	}
	res := runCheck(t, p, nil)
	if res.Status != report.StatusSkipped || !strings.Contains(res.SkipReason, "root") {
		t.Fatalf("status %s reason %q", res.Status, res.SkipReason)
	}
}
