package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tidyit/internal/engine"
)

func write(t *testing.T, path string, size int, age time.Duration) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
	ts := time.Now().Add(-age)
	if err := os.Chtimes(path, ts, ts); err != nil {
		t.Fatal(err)
	}
}

func scan(t *testing.T, opts map[string]any) []string {
	t.Helper()
	spec, _ := Paths().Manifest.Check("scan")
	vals, err := spec.Resolve(opts)
	if err != nil {
		t.Fatal(err)
	}
	out, err := pathScan(context.Background(), engine.Input{Options: vals})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, it := range out.Items {
		names = append(names, filepath.Base(it.Path))
	}
	return names
}

func TestPathScanEntriesUsesNewestFileInFolder(t *testing.T) {
	root := t.TempDir()
	day := 24 * time.Hour
	write(t, filepath.Join(root, "old.iso"), 10, 100*day)
	write(t, filepath.Join(root, "new.iso"), 10, 1*day)
	write(t, filepath.Join(root, "notes.txt"), 10, 100*day)
	// A folder with one recent file inside is not stale, even if old itself.
	write(t, filepath.Join(root, "proj", "a"), 10, 200*day)
	write(t, filepath.Join(root, "proj", "b"), 10, 2*day)
	write(t, filepath.Join(root, "dead", "a"), 10, 200*day)
	os.Chtimes(filepath.Join(root, "proj"), time.Now().Add(-300*day), time.Now().Add(-300*day))
	os.Chtimes(filepath.Join(root, "dead"), time.Now().Add(-300*day), time.Now().Add(-300*day))

	got := strings.Join(scan(t, map[string]any{"paths": []string{root}, "min_age_days": 90}), ",")
	for _, want := range []string{"old.iso", "notes.txt", "dead"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s in %s", want, got)
		}
	}
	for _, not := range []string{"new.iso", "proj"} {
		if strings.Contains(got, not) {
			t.Errorf("%s should not be flagged: %s", not, got)
		}
	}

	got = strings.Join(scan(t, map[string]any{"paths": []string{root}, "patterns": []string{"*.iso"}}), ",")
	if got != "new.iso,old.iso" && got != "old.iso,new.iso" {
		t.Errorf("patterns: got %s", got)
	}
	got = strings.Join(scan(t, map[string]any{"paths": []string{root}, "type": "dir", "exclude": []string{"proj"}}), ",")
	if got != "dead" {
		t.Errorf("type=dir with exclude: got %s", got)
	}
}

func TestPathScanFilesModeFindsLargeFilesAtAnyDepth(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "a", "b", "big.bin"), 2<<20, 0)
	write(t, filepath.Join(root, "small.bin"), 1024, 0)
	write(t, filepath.Join(root, "node_modules", "huge.bin"), 3<<20, 0)
	write(t, filepath.Join(root, "x", "skip", "huge2.bin"), 3<<20, 0)
	got := scan(t, map[string]any{"paths": []string{root}, "mode": "files", "min_size_mb": 1,
		"exclude": []string{"node_modules", "x/skip"}})
	if strings.Join(got, ",") != "big.bin" {
		t.Errorf("got %v", got)
	}
}

func TestPathScanNeedsPaths(t *testing.T) {
	spec, _ := Paths().Manifest.Check("scan")
	vals, _ := spec.Resolve()
	if _, err := pathScan(context.Background(), engine.Input{Options: vals}); err == nil {
		t.Fatal("expected an error with no paths")
	}
}

func TestExecProgram(t *testing.T) {
	cases := map[string]string{
		"obsidian --hidden %U":                     "obsidian",
		"env GDK_BACKEND=x11 -u FOO slack -u":      "slack",
		`"/opt/My App/run" --flag`:                 "/opt/My App/run",
		"/usr/bin/flatpak run --command=x org.App": "/usr/bin/flatpak",
		"": "",
	}
	for in, want := range cases {
		if got := execProgram(in); got != want {
			t.Errorf("execProgram(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAutostartBroken(t *testing.T) {
	cfgHome, sysDir := t.TempDir(), t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgHome)
	t.Setenv("XDG_CONFIG_DIRS", sysDir)
	entry := func(dir, file, body string) {
		write(t, filepath.Join(dir, "autostart", file), 0, 0)
		os.WriteFile(filepath.Join(dir, "autostart", file), []byte("[Desktop Entry]\n"+body), 0o644)
	}
	entry(cfgHome, "gone.desktop", "Name=Gone\nExec=no-such-program-tidyit --x\n")
	entry(cfgHome, "ok.desktop", "Exec=sh -c true\n")
	entry(cfgHome, "hidden.desktop", "Exec=no-such-program-tidyit\nHidden=true\n")
	entry(cfgHome, "tryexec.desktop", "TryExec=no-such-tryexec-tidyit\nExec=sh\n")
	entry(sysDir, "sys-broken.desktop", "Exec=no-such-sys-tidyit\n")
	entry(sysDir, "sys-other-desktop.desktop", "TryExec=no-such-tryexec-tidyit\nExec=no-such-tryexec-tidyit\n")
	entry(sysDir, "overridden.desktop", "Exec=no-such-program-tidyit\n")
	entry(cfgHome, "overridden.desktop", "Exec=sh\n")

	spec := Autostart().Manifest.Checks[0]
	vals, _ := spec.Resolve(map[string]any{"include_system": true})
	out, err := autostartBroken(context.Background(), engine.Input{Options: vals})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, it := range out.Items {
		got[filepath.Base(it.Path)] = string(it.Confidence)
	}
	want := map[string]string{"gone.desktop": "safe", "tryexec.desktop": "safe", "sys-broken.desktop": "manual"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: got %q, want %q (all: %v)", k, got[k], v, got)
		}
	}
}

func TestReadMountsDedupesAndUnescapes(t *testing.T) {
	p := filepath.Join(t.TempDir(), "mounts")
	os.WriteFile(p, []byte(`/dev/sda2 / ext4 rw 0 0
tmpfs /run tmpfs rw 0 0
/dev/sdb1 /home/me/.snapshots btrfs rw 0 0
/dev/sdb1 /home btrfs rw 0 0
/dev/sdc1 /media/My\040Disk exfat rw 0 0
`), 0o644)
	ms, err := readMounts(p)
	if err != nil {
		t.Fatal(err)
	}
	var dirs []string
	for _, m := range ms {
		dirs = append(dirs, m.dir)
	}
	if got := strings.Join(dirs, "|"); got != "/|/home|/media/My Disk" {
		t.Errorf("got %s", got)
	}
}
