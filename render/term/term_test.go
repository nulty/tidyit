package term

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"tidyit/pkg/report"
)

func sample() (*report.Report, []Group) {
	start := time.Date(2026, 11, 10, 17, 0, 0, 0, time.UTC)
	rep := &report.Report{Host: "box", StartedAt: start, FinishedAt: start.Add(2 * time.Second)}
	items := func(conf report.Confidence, sizes ...int64) []report.Item {
		var out []report.Item
		for i, s := range sizes {
			id := string(rune('a' + i))
			out = append(out, report.Item{ID: id, Label: "item-" + id, Path: "/x/" + string(conf) + "/" + id, Size: s, Confidence: conf,
				Action: &report.Action{Command: "rm " + id}})
		}
		return out
	}
	groups := []Group{
		{Name: "Disk", Results: []report.Result{{ID: "disk.usage", Title: "Filesystem usage", Status: report.StatusFlag,
			Items: []report.Item{{ID: "/", Label: "/", Detail: "89% full, 24G free", Size: 1 << 30, Confidence: report.ConfidenceManual}}}}},
		{Name: "Small", Results: []report.Result{{ID: "small", Title: "Small things", Status: report.StatusFlag, Items: items(report.ConfidenceSafe, 1<<20)}}},
		{Name: "Big", Results: []report.Result{
			{ID: "big", Title: "Big things", Status: report.StatusFlag, Items: items(report.ConfidenceReview, 5<<30, 1<<30, 1<<29, 1<<28, 1<<20)},
			{ID: "fine", Title: "Fine check", Status: report.StatusOK},
			{ID: "gone", Title: "Missing tool", Status: report.StatusSkipped, SkipReason: "tool not installed"},
			{ID: "bad", Title: "Broken check", Status: report.StatusError, Error: "exploded"},
		}},
	}
	for _, g := range groups {
		rep.Results = append(rep.Results, g.Results...)
	}
	return rep, groups
}

func TestCompact(t *testing.T) {
	rep, groups := sample()
	var b bytes.Buffer
	Render(&b, rep, groups, Options{Headline: []string{"disk.usage"}})
	out := b.String()
	for _, want := range []string{
		"/ 89% full, 24G free",
		"1.0M safe to remove",
		"6.8G to review", // 5G+1G+512M+256M+1M
		"item-a 5.0G, item-b 1.0G, item-c 512M, +2",
		"ok    Fine check",
		"Missing tool: tool not installed",
		"error",
		"exploded",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Filesystem usage") {
		t.Errorf("headline result also shown as a row:\n%s", out)
	}
	if strings.Index(out, "Big things") > strings.Index(out, "Small things") {
		t.Errorf("rows not sorted by size:\n%s", out)
	}
	if strings.Contains(out, "rm a") {
		t.Errorf("compact view shows commands:\n%s", out)
	}
}

func TestCompactFitsWidth(t *testing.T) {
	rep, groups := sample()
	var b bytes.Buffer
	Render(&b, rep, groups, Options{Width: 60, OnlyFlagged: true})
	for _, line := range strings.Split(strings.TrimRight(b.String(), "\n"), "\n") {
		if strings.HasPrefix(line, "details:") {
			continue
		}
		if n := len([]rune(line)); n > 60 {
			t.Errorf("line is %d wide: %q", n, line)
		}
	}
	if !strings.Contains(b.String(), "+4") {
		t.Errorf("narrow row lost its count:\n%s", b.String())
	}
	if strings.Contains(b.String(), "Fine check") {
		t.Errorf("OnlyFlagged still shows ok results")
	}
}

func TestFullCapsItemsAndShowsCommands(t *testing.T) {
	rep, groups := sample()
	big := &groups[2].Results[0]
	for i := 0; i < 12; i++ {
		big.Items = append(big.Items, report.Item{ID: "extra" + string(rune('a'+i)), Label: "extra", Size: 1, Confidence: report.ConfidenceReview})
	}
	var b bytes.Buffer
	Render(&b, rep, groups, Options{Full: true})
	out := b.String()
	if !strings.Contains(out, "$ rm a") || !strings.Contains(out, "and 7 more") {
		t.Errorf("full view:\n%s", out)
	}
	b.Reset()
	Render(&b, rep, groups, Options{Verbose: true})
	if strings.Contains(b.String(), "and 7 more") {
		t.Errorf("verbose view capped items")
	}
}
