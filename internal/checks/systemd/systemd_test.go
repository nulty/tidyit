package systemd

import (
	"reflect"
	"testing"
)

func TestParseFailed(t *testing.T) {
	out := []byte("mise-lockfiles-perms.path    loaded failed failed Watch shared mise lockfiles\nfoo.service loaded failed failed\n")
	want := []unit{{"mise-lockfiles-perms.path", "Watch shared mise lockfiles"}, {"foo.service", ""}}
	if got := parseFailed(out); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
}

func TestParseDiskUsage(t *testing.T) {
	mb740 := 740.3 * 1024 * 1024
	cases := map[string]int64{
		"Archived and active journals take up 740.3M in the file system.": int64(mb740),
		"Archived and active journals take up 2.0G in the file system.":   2 << 30,
		"Archived and active journals take up 512B in the file system.":   512,
	}
	for in, want := range cases {
		if got, ok := parseDiskUsage([]byte(in)); !ok || got != want {
			t.Errorf("%q: got %d %v, want %d", in, got, ok, want)
		}
	}
	if _, ok := parseDiskUsage([]byte("No journal files were found.")); ok {
		t.Error("parsed nonsense")
	}
}
