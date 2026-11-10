// Package htmlreport renders a report as one self-contained HTML file:
// inline CSS and JS, no network requests, and the full JSON report
// embedded in a <script type="application/json" id="tidyit-report"> tag so
// other tools can read it back.
package htmlreport

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"os"
	"path/filepath"
	"strings"

	"tidyit/internal/fsx"
	"tidyit/pkg/report"
	"tidyit/render/view"
)

//go:embed report.html.tmpl
var tmplText string

var tmpl = template.Must(template.New("report").Parse(tmplText))

// Options control the output.
type Options struct {
	// Headline lists result ids summarised at the top rather than listed,
	// e.g. disk usage.
	Headline []string
}

type item struct {
	Label, Path, Size, Detail, Conf, Command string
	NeedsRoot                                bool
	ShowConf                                 bool // the check's items differ in confidence
}

type check struct {
	ID, Title, Conf, Size, Summary, Hint, Preview string
	Count                                         int
	Mixed                                         bool // items have different confidence
	Notes                                         []string
	Items                                         []item
}

type quiet struct {
	ID, Title, Text string
	Notes           []string
}

type data struct {
	Host, Date, Duration string
	Generator            string
	Headlines            []string
	Safe, Review         string
	Checks               []check
	Errors, OK, Skipped  []quiet
	ConfCounts           map[string]int // flagged checks by confidence, for the filter
	JSON                 template.JS
}

// Render writes the report as HTML.
func Render(w io.Writer, rep *report.Report, groups []view.Group, opt Options) error {
	o := view.Build(groups, opt.Headline)
	raw, err := json.Marshal(rep) // escapes <, > and & so it can't end the script tag
	if err != nil {
		return err
	}
	d := data{
		Host:       rep.Host,
		Generator:  rep.Generator,
		Date:       rep.StartedAt.Format("2006-01-02 15:04"),
		Duration:   rep.FinishedAt.Sub(rep.StartedAt).Round(100_000_000).String(),
		Headlines:  o.Headlines,
		ConfCounts: map[string]int{},
		JSON:       template.JS(raw),
	}
	if d.Generator == "" {
		d.Generator = "tidyit"
	}
	if o.Safe > 0 {
		d.Safe = fsx.Human(o.Safe)
	}
	if o.Review > 0 {
		d.Review = fsx.Human(o.Review)
	}
	for _, r := range o.Flagged {
		c := check{ID: r.ID, Title: r.Title, Conf: string(r.Caution()), Summary: r.Summary, Hint: r.Hint,
			Count: len(r.Items), Notes: r.Notes, Preview: preview(r.Items)}
		if n := r.TotalSize(); n > 0 {
			c.Size = fsx.Human(n)
		}
		for _, it := range r.Items {
			if string(it.Confidence) != c.Conf {
				c.Mixed = true
			}
			ci := item{Label: it.Label, Path: it.Path, Detail: it.Detail, Conf: string(it.Confidence)}
			if it.Size > 0 {
				ci.Size = fsx.Human(it.Size)
			}
			if it.Action != nil {
				ci.Command, ci.NeedsRoot = it.Action.Command, it.Action.NeedsRoot
			}
			c.Items = append(c.Items, ci)
		}
		if c.Mixed {
			for i := range c.Items {
				c.Items[i].ShowConf = true
			}
		}
		d.ConfCounts[c.Conf]++
		d.Checks = append(d.Checks, c)
	}
	for _, r := range o.Errors {
		d.Errors = append(d.Errors, quiet{ID: r.ID, Title: r.Title, Text: r.Error})
	}
	for _, r := range o.OK {
		d.OK = append(d.OK, quiet{ID: r.ID, Title: r.Title, Text: r.Summary, Notes: r.Notes})
	}
	for _, r := range o.Skipped {
		d.Skipped = append(d.Skipped, quiet{ID: r.ID, Title: r.Title, Text: r.SkipReason})
	}
	return tmpl.Execute(w, d)
}

// preview names the first few items, e.g. "kf6-core24, gnome-46-2404 and 15 more".
func preview(items []report.Item) string {
	const n = 3
	var names []string
	for i, it := range items {
		if i == n {
			break
		}
		names = append(names, it.Label)
	}
	s := strings.Join(names, ", ")
	if rest := len(items) - len(names); rest > 0 {
		s += fmt.Sprintf(" and %d more", rest)
	}
	return s
}

// WriteFile writes the report to path. It writes a temporary file in the
// same folder first, so a failed run never leaves half a report.
func WriteFile(path string, rep *report.Report, groups []view.Group, opt Options) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tidyit-report-*.html")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := Render(tmp, rep, groups, opt); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
