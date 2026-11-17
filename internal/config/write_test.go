package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestMarshalRoundTrips(t *testing.T) {
	on, off := true, false
	cfg := &Config{
		Run: Run{Concurrency: 4, Timeout: 30 * time.Second},
		Checks: map[string]*Entry{
			"docker":               {Enabled: &off, Options: map[string]any{}},
			"docker.unused-images": {Enabled: &on, Options: map[string]any{"min_age_days": int64(60), "ignore": []any{"postgres:*"}}},
			"old-isos":             {Use: "paths.scan", Title: "Old ISOs", Timeout: time.Minute, Options: map[string]any{"paths": []any{"~/Downloads"}}},
			"odd id":               {Enabled: &off, Options: map[string]any{}},
		},
	}
	data, err := cfg.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(data), Header) {
		t.Fatalf("no header:\n%s", data)
	}
	p := filepath.Join(t.TempDir(), "config.toml")
	write(t, p, string(data))
	got, err := Load(p, true)
	if err != nil {
		t.Fatalf("written config does not load: %v\n%s", err, data)
	}
	got.Path = ""
	if !reflect.DeepEqual(got, cfg) {
		t.Errorf("round trip changed the config\nwrote:\n%s\ngot:  %#v\nwant: %#v", data, got, cfg)
	}
}

func TestMarshalEmptyIsJustTheHeader(t *testing.T) {
	data, err := (&Config{}).Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != headerText {
		t.Errorf("got %q", data)
	}
}

func TestSettersPruneEmptyEntries(t *testing.T) {
	cfg := &Config{}
	on := true
	cfg.SetEnabled("a.b", &on)
	cfg.SetOption("a.b", "n", 5)
	cfg.SetOption("a.b", "n", nil)
	if e := cfg.Checks["a.b"]; e == nil || *e.Enabled != true || len(e.Options) != 0 {
		t.Fatalf("after removing the option, enabled should remain: %+v", e)
	}
	cfg.SetEnabled("a.b", nil)
	if _, ok := cfg.Checks["a.b"]; ok {
		t.Errorf("an entry with nothing set should be removed")
	}

	cfg.Checks["mine"] = &Entry{Use: "paths.scan", Enabled: &on, Options: map[string]any{"paths": []any{"~"}}}
	cfg.Reset("mine")
	if e := cfg.Checks["mine"]; e == nil || e.Use != "paths.scan" || e.Enabled != nil || len(e.Options) != 0 {
		t.Errorf("Reset should keep a user instance's use and drop the rest: %+v", e)
	}
}

func TestUpdateCreatesFileAndDirs(t *testing.T) {
	p := filepath.Join(t.TempDir(), "tidyit", "config.toml")
	backup, err := Update(p, func(c *Config) error {
		c.SetOption("trash.size", "min_size_mb", 500)
		return nil
	})
	if err != nil || backup != "" {
		t.Fatalf("backup=%q err=%v", backup, err)
	}
	cfg, err := Load(p, true)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Checks["trash.size"].Options["min_size_mb"]; got != int64(500) {
		t.Errorf("min_size_mb = %#v", got)
	}
}

func TestUpdateBacksUpHandWrittenFileOnce(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.toml")
	hand := "# my notes\n[checks.docker]\nenabled = false\n"
	write(t, p, hand)

	set := func(n int) func(*Config) error {
		return func(c *Config) error { c.SetOption("trash.size", "min_size_mb", n); return nil }
	}
	backup, err := Update(p, set(1))
	if err != nil {
		t.Fatal(err)
	}
	if backup != p+".bak" || read(t, backup) != hand {
		t.Fatalf("backup %q should hold the hand-written file", backup)
	}
	if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want the original 0600 kept", fi.Mode().Perm())
	}
	cfg, _ := Load(p, true)
	if e := cfg.Checks["docker"]; e == nil || *e.Enabled {
		t.Errorf("hand-written settings must survive the rewrite")
	}

	// Now managed: no second backup.
	if backup, err = Update(p, set(2)); err != nil || backup != "" {
		t.Errorf("second update: backup=%q err=%v", backup, err)
	}

	// Hand-edited again without the header: a new backup, the old one kept.
	write(t, p, "[checks.trash.size]\nmin_size_mb = 3\n")
	if backup, err = Update(p, set(4)); err != nil || backup != p+".bak.2" {
		t.Errorf("third update: backup=%q err=%v", backup, err)
	}
	if read(t, p+".bak") != hand {
		t.Errorf("first backup was overwritten")
	}
}

func TestUpdateLeavesFileAloneOnError(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	write(t, p, "[checks.docker\n") // broken
	if _, err := Update(p, func(*Config) error { return nil }); err == nil {
		t.Fatal("want a parse error")
	}
	if read(t, p) != "[checks.docker\n" {
		t.Error("a file that does not parse must not be touched")
	}
	entries, _ := os.ReadDir(filepath.Dir(p))
	if len(entries) != 1 {
		t.Errorf("stray files left: %v", entries)
	}
}

func TestUpdateWritesThroughSymlink(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "dotfiles.toml")
	write(t, real, Header+"\n")
	link := filepath.Join(dir, "config.toml")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Update(link, func(c *Config) error { c.SetOption("a.b", "n", 1); return nil }); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Lstat(link); fi.Mode()&os.ModeSymlink == 0 {
		t.Error("the symlink was replaced by a file")
	}
	if !strings.Contains(read(t, real), "n = 1") {
		t.Errorf("target not updated:\n%s", read(t, real))
	}
}
