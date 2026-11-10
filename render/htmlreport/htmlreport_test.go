package htmlreport

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"

	"tidy/pkg/report"
	"tidy/render/view"
)

func sample() (*report.Report, []view.Group) {
	start := time.Date(2026, 11, 10, 17, 0, 0, 0, time.UTC)
	rep := &report.Report{SchemaVersion: 1, Host: "box", StartedAt: start, FinishedAt: start.Add(time.Second)}
	results := []report.Result{
		{ID: "disk.usage", Title: "Disk usage", Status: report.StatusFlag,
			Items: []report.Item{{ID: "/", Label: "/", Detail: "89% full, 24G free", Confidence: report.ConfidenceManual}}},
		{ID: "evil", Title: "Evil <b>check</b>", Status: report.StatusFlag, Hint: "rm -rf </script><script>alert(1)</script>",
			Items: []report.Item{
				{ID: "a", Label: `<img src=x onerror=alert(1)>`, Size: 2 << 30, Confidence: report.ConfidenceSafe,
					Action: &report.Action{Command: `rm "</script>"`, NeedsRoot: true}},
				{ID: "b", Label: "plain", Size: 1 << 20, Confidence: report.ConfidenceReview},
			}},
		{ID: "fine", Title: "Fine check", Status: report.StatusOK, Summary: "740M", Notes: []string{"a note"}},
		{ID: "gone", Title: "Missing tool", Status: report.StatusSkipped, SkipReason: "tool not installed"},
		{ID: "bad", Title: "Broken check", Status: report.StatusError, Error: "exploded"},
	}
	rep.Results = results
	return rep, []view.Group{{Name: "All", Results: results}}
}

func render(t *testing.T) string {
	t.Helper()
	rep, groups := sample()
	var b bytes.Buffer
	if err := Render(&b, rep, groups, Options{Headline: []string{"disk.usage"}}); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func TestRenderContent(t *testing.T) {
	out := render(t)
	for _, want := range []string{
		"/ 89% full, 24G free", // headline
		"2.0G",                 // safe total card
		"Evil &lt;b&gt;check&lt;/b&gt;",
		"<th>Confidence</th>", // mixed confidence column
		`title="needs root"`,
		"Fine check", "740M", "a note",
		"Missing tool", "tool not installed",
		"Broken check", "exploded",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(out, ">Disk usage<") {
		t.Error("headline result also listed as a check")
	}
}

func TestRenderEscapes(t *testing.T) {
	out := render(t)
	if strings.Contains(out, "<img src=x") {
		t.Error("item label not escaped")
	}
	// Only our own two script tags may close: the JSON one and the JS one.
	if n := strings.Count(out, "</script>"); n != 2 {
		t.Errorf("found %d </script> tags, want 2", n)
	}
}

func TestEmbeddedJSONRoundTrips(t *testing.T) {
	out := render(t)
	m := regexp.MustCompile(`(?s)<script type="application/json" id="tidy-report">(.*?)</script>`).FindStringSubmatch(out)
	if m == nil {
		t.Fatal("no embedded JSON")
	}
	var got report.Report
	if err := json.Unmarshal([]byte(m[1]), &got); err != nil {
		t.Fatalf("embedded JSON does not parse: %v", err)
	}
	want, _ := sample()
	if got.Host != want.Host || len(got.Results) != len(want.Results) || got.Results[1].Items[0].Action.Command != `rm "</script>"` {
		t.Fatalf("round trip lost data: %+v", got)
	}
}

func TestRenderEmptyReport(t *testing.T) {
	var b bytes.Buffer
	rep := &report.Report{Host: "box"}
	if err := Render(&b, rep, nil, Options{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "Tidy report") {
		t.Fatal("empty report did not render")
	}
}
