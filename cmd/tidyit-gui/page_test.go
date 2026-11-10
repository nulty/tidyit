package main

import (
	"reflect"
	"testing"

	"tidyit/pkg/report"
)

func TestItemSubtitle(t *testing.T) {
	it := report.Item{Label: "kf6-core24", Path: "/var/lib/snapd/snaps/kf6-core24_64.snap", Detail: "revision 64"}
	if got, want := itemSubtitle(it), "revision 64 · /var/lib/snapd/snaps/kf6-core24_64.snap"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestItemSubtitleSkipsPathShownInLabel(t *testing.T) {
	it := report.Item{Label: "/h/Downloads/x.iso", Path: "/h/Downloads/x.iso"}
	if got := itemSubtitle(it); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

func TestGroupByConfidence(t *testing.T) {
	res := func(id string, confs ...report.Confidence) report.Result {
		r := report.Result{ID: id}
		for _, c := range confs {
			r.Items = append(r.Items, report.Item{Confidence: c})
		}
		return r
	}
	flagged := []report.Result{
		res("a", report.ConfidenceReview),
		res("b", report.ConfidenceSafe),
		res("mixed", report.ConfidenceSafe, report.ConfidenceReview),
		res("c", report.ConfidenceSafe),
	}
	var got [][]string
	for _, g := range groupByConfidence(flagged) {
		ids := []string{g.Name}
		for _, r := range g.Results {
			ids = append(ids, r.ID)
		}
		got = append(got, ids)
	}
	want := [][]string{{"Safe to remove", "b", "c"}, {"Worth reviewing", "a", "mixed"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestCheckFacts(t *testing.T) {
	r := report.Result{Items: []report.Item{{Size: 3 << 30}, {Size: 1 << 30}}}
	if got, want := checkFacts(r), "4.0G · 2 items"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	r.Items = []report.Item{{}}
	if got, want := checkFacts(r), "1 item"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestDiskUsage(t *testing.T) {
	results := []report.Result{{
		ID: "disk.usage", Status: report.StatusFlag, Summary: "at or above 85% full",
		Items: []report.Item{{Label: "/media/My Disk"}},
		Notes: []string{
			"/             45% used    100G free of 200G",
			"/media/My Disk 91% used   9.0G free of 100G",
		},
	}}
	p, others, sum := diskUsage(results)
	want := mountUsage{Mount: "/media/My Disk", Percent: 91, Free: "9.0G", Flagged: true}
	if p == nil || *p != want || len(others) != 0 || sum != "at or above 85% full" {
		t.Fatalf("got %+v %+v %q", p, others, sum)
	}

	results[0].Status, results[0].Items, results[0].Summary = report.StatusOK, nil, ""
	p, _, sum = diskUsage(results)
	if p == nil || p.Mount != "/" || p.Percent != 45 || p.Flagged || sum != "" {
		t.Fatalf("unflagged: got %+v %q", p, sum)
	}

	if p, _, _ := diskUsage(nil); p != nil {
		t.Fatalf("no disk check: got %+v", p)
	}
}

func TestMixed(t *testing.T) {
	same := []report.Item{{Confidence: "safe"}, {Confidence: "safe"}}
	diff := []report.Item{{Confidence: "safe"}, {Confidence: "review"}}
	if mixed(same) || !mixed(diff) || mixed(nil) {
		t.Fatal("mixed is wrong")
	}
}

func TestSentence(t *testing.T) {
	for in, want := range map[string]string{"": "", "flatpak not installed": "Flatpak not installed", "ébc": "Ébc"} {
		if got := sentence(in); got != want {
			t.Errorf("sentence(%q) = %q, want %q", in, got, want)
		}
	}
}
