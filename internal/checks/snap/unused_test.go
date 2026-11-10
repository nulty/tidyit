package snap

import (
	"encoding/json"
	"reflect"
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
