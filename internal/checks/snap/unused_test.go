package snap

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Fixtures shaped like snapd's /v2/snaps and /v2/connections?select=all.
const snapsJSON = `[
 {"name":"firefox","type":"app","base":"core24","revision":"1","status":"active","apps":[{"name":"firefox"}]},
 {"name":"okular","type":"app","base":"core24","revision":"1","status":"active","apps":[{"name":"okular"}]},
 {"name":"core24","type":"base","revision":"1","status":"active"},
 {"name":"core22","type":"base","revision":"1","status":"active"},
 {"name":"core20","type":"base","revision":"1","status":"active"},
 {"name":"bare","type":"base","revision":"1","status":"active"},
 {"name":"snapd","type":"snapd","revision":"1","status":"active"},
 {"name":"gnome-46-2404","type":"app","base":"core24","revision":"1","status":"active"},
 {"name":"gnome-42-2204","type":"app","base":"core22","revision":"1","status":"active"},
 {"name":"lxqt-support-core24","type":"app","base":"core24","revision":"1","status":"active"},
 {"name":"gtk-common-themes","type":"app","base":"bare","revision":"1","status":"active"},
 {"name":"hunspell","type":"app","base":"bare","revision":"1","status":"active"},
 {"name":"mesa-2404","type":"app","base":"core24","revision":"1","status":"active","apps":[{"name":"helper"}]},
 {"name":"lonely-app","type":"app","base":"core24","revision":"1","status":"active","apps":[{"name":"x"}]}
]`

const connsJSON = `{
 "established":[
  {"plug":{"snap":"firefox","plug":"gnome-46-2404"},"slot":{"snap":"gnome-46-2404","slot":"gnome-46-2404"},"interface":"content"},
  {"plug":{"snap":"firefox","plug":"gtk-3-themes"},"slot":{"snap":"gtk-common-themes","slot":"gtk-3-themes"},"interface":"content"},
  {"plug":{"snap":"firefox","plug":"home"},"slot":{"snap":"snapd","slot":"home"},"interface":"home"}
 ],
 "plugs":[
  {"snap":"okular","interface":"content","attrs":{"default-provider":"lxqt-support-core24"}},
  {"snap":"firefox","interface":"content","attrs":{"default-provider":"mesa-2404:gpu-2404"}}
 ],
 "slots":[
  {"snap":"gnome-46-2404","interface":"content"},
  {"snap":"gnome-42-2204","interface":"content"},
  {"snap":"lxqt-support-core24","interface":"content"},
  {"snap":"gtk-common-themes","interface":"content"},
  {"snap":"hunspell","interface":"content"},
  {"snap":"mesa-2404","interface":"content"}
 ]
}`

func TestFindUnused(t *testing.T) {
	var snaps []snapInfo
	var conns connections
	if err := json.Unmarshal([]byte(snapsJSON), &snaps); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(connsJSON), &conns); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, u := range findUnused(snaps, conns) {
		got = append(got, u.snap.Name+":"+u.kindDetail())
	}
	// core20: no users. gnome-42-2204: content snap no one uses; core22 is
	// only its base, so it follows. hunspell: unused content snap (bare
	// stays, gtk-common-themes uses it). lxqt is wanted by an unconnected
	// okular plug, mesa by firefox, so neither is flagged; nor are apps.
	want := []string{
		"core20:, nothing uses it",
		"gnome-42-2204:, nothing uses it",
		"hunspell:, nothing uses it",
		"core22:, used only by gnome-42-2204 (also unused)",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}

func TestSeedName(t *testing.T) {
	for line, want := range map[string]string{
		"  - name: core22":      "core22",
		"    name: \"firefox\"": "firefox",
		"    channel: stable":   "",
	} {
		m := seedName.FindStringSubmatch(line)
		got := ""
		if m != nil {
			got = m[1]
		}
		if got != want {
			t.Errorf("%q: got %q want %q", line, got, want)
		}
	}
}

func TestFindNeverOpened(t *testing.T) {
	var snaps []snapInfo
	var conns connections
	if err := json.Unmarshal([]byte(snapsJSON), &snaps); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(connsJSON), &conns); err != nil {
		t.Fatal(err)
	}
	// A system service snap that was never "opened" must not be flagged.
	snaps = append(snaps, snapInfo{Name: "cups", Type: "app", Base: "core24", Status: "active",
		Apps: []struct {
			Name        string `json:"name"`
			Daemon      string `json:"daemon"`
			DaemonScope string `json:"daemon-scope"`
		}{{Name: "cupsd", Daemon: "simple", DaemonScope: "system"}, {Name: "lp"}}})
	opened := map[string]bool{"firefox": true}
	got := map[string][]string{}
	for _, no := range findNeverOpened(snaps, conns, func(n string) bool { return opened[n] }) {
		var frees []string
		for _, f := range no.frees {
			frees = append(frees, f.Name)
		}
		got[no.snap.Name] = frees
	}
	// okular alone needs lxqt-support-core24; lonely-app needs nothing
	// extra (core24 is still used). mesa-2404 is used by firefox; cups runs
	// a system service; firefox has been opened.
	want := map[string][]string{"okular": {"lxqt-support-core24"}, "lonely-app": nil}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestAccountSnapDirs(t *testing.T) {
	dir := t.TempDir()
	mk := func(p string, mode os.FileMode) {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, mode); err != nil {
			t.Fatal(err)
		}
	}
	mk(filepath.Join(dir, "alice", "snap", "okular"), 0o755)
	mk(filepath.Join(dir, "bob"), 0o755) // never ran a snap
	mk(filepath.Join(dir, "carol"), 0o000)
	t.Cleanup(func() { os.Chmod(filepath.Join(dir, "carol"), 0o755) })
	passwd := filepath.Join(dir, "passwd")
	lines := []string{
		"root:x:0:0:root:" + filepath.Join(dir, "carol") + ":/bin/bash",     // root is never counted
		"ghost:x:1004:1004::" + filepath.Join(dir, "nohome") + ":/bin/bash", // no home folder
		"daemon:x:1:1::/usr/sbin:/usr/sbin/nologin",
		"alice:x:1000:1000::" + filepath.Join(dir, "alice") + ":/bin/bash",
		"bob:x:1001:1001::" + filepath.Join(dir, "bob") + ":/bin/zsh",
		"carol:x:1002:1002::" + filepath.Join(dir, "carol") + ":/bin/bash",
		"svc:x:1003:1003::" + filepath.Join(dir, "alice") + ":/usr/bin/false",
		"nobody:x:65534:65534::/nonexistent:/usr/sbin/nologin",
	}
	if err := os.WriteFile(passwd, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	readable, private := accountSnapDirs(passwd)
	if len(readable) != 2 || !strings.HasSuffix(readable[0], "alice/snap") || !strings.HasSuffix(readable[1], "bob/snap") {
		t.Fatalf("readable %v", readable)
	}
	if os.Geteuid() != 0 && !reflect.DeepEqual(private, []string{"carol"}) {
		t.Fatalf("private %v", private)
	}
}
