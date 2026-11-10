package bleachbit

import (
	"strings"
	"testing"

	"tidy/internal/fsx"
)

func TestParsePreviewGroupsByFolder(t *testing.T) {
	h := fsx.Home()
	out := strings.Join([]string{
		"Delete 16.4kB " + h + "/.config/google-chrome/Default/Network Persistent State",
		"Delete 1.5MB " + h + "/.config/google-chrome/ShaderCache/data_1",
		"Clean file ?B " + h + "/.config/google-chrome/Default/Preferences",
		"Vacuum ?B " + h + "/.config/google-chrome/Default/History",
		"Delete 8.2kB " + h + "/.cache/thumbnails/normal/a.png",
		"Delete 8.2kB " + h + "/.cache/thumbnails/normal/b.png",
		"Delete 1kB " + h + "/.cache/loose-file",
		"Delete 2kB /var/cache/thing/x",
		"",
		"Disk space to be recovered: 1.55MB",
		"Files to be deleted: 6",
		"Special operations: 2",
	}, "\n")
	s := parsePreview(strings.NewReader(out))
	if s.files != 6 || s.special != 2 || s.recover != "1.55MB" {
		t.Fatalf("totals: files %d special %d recover %q", s.files, s.special, s.recover)
	}
	want := map[string]struct {
		size           int64
		files, special int
	}{
		h + "/.config/google-chrome": {1_516_400, 2, 2},
		h + "/.cache/thumbnails":     {16_400, 2, 0},
		h + "/.cache":                {1_000, 1, 0},
		"/var/cache":                 {2_000, 1, 0},
	}
	if len(s.groups) != len(want) {
		t.Fatalf("groups: %v", s.sortedGroups())
	}
	for k, w := range want {
		g := s.groups[k]
		if g == nil || g.size != w.size || g.files != w.files || g.special != w.special {
			t.Errorf("%s: got %+v, want %+v", k, g, w)
		}
	}
	if first := s.sortedGroups()[0].path; first != h+"/.config/google-chrome" {
		t.Errorf("largest first: got %s", first)
	}
}
