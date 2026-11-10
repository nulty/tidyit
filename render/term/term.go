// Package term renders a report for a terminal.
package term

import (
	"fmt"
	"io"
	"strings"

	"tidy/internal/fsx"
	"tidy/pkg/report"
)

// Group is a titled set of results, e.g. all results of one plugin.
type Group struct {
	Name    string
	Results []report.Result
}

// Options control the output.
type Options struct {
	Color       bool
	OnlyFlagged bool // hide ok and skipped results
	Verbose     bool // show durations and notes for every result
}

type palette struct{ bold, dim, yel, grn, red, cyan, rst string }

func newPalette(on bool) palette {
	if !on {
		return palette{}
	}
	return palette{"\033[1m", "\033[2m", "\033[33m", "\033[32m", "\033[31m", "\033[36m", "\033[0m"}
}

// Render writes the report.
func Render(w io.Writer, rep *report.Report, groups []Group, opt Options) {
	c := newPalette(opt.Color)
	fmt.Fprintf(w, "%sTidy report%s  %s  %s%s on %s%s\n", c.bold, c.rst,
		rep.StartedAt.Format("2006-01-02 15:04"), c.dim, rep.FinishedAt.Sub(rep.StartedAt).Round(100_000_000), rep.Host, c.rst)

	counts := map[report.Status]int{}
	var all []report.Result
	for _, g := range groups {
		var shown []report.Result
		for _, r := range g.Results {
			counts[r.Status]++
			all = append(all, r)
			if opt.OnlyFlagged && (r.Status == report.StatusOK || r.Status == report.StatusSkipped) {
				continue
			}
			shown = append(shown, r)
		}
		if len(shown) == 0 {
			continue
		}
		fmt.Fprintf(w, "\n%s== %s ==%s\n", c.bold, g.Name, c.rst)
		for _, r := range shown {
			renderResult(w, r, c, opt)
		}
	}

	fmt.Fprintf(w, "\n%s%d flagged%s · %d ok · %d skipped", c.yel, counts[report.StatusFlag], c.rst,
		counts[report.StatusOK], counts[report.StatusSkipped])
	if n := counts[report.StatusError]; n > 0 {
		fmt.Fprintf(w, " · %s%d errors%s", c.red, n, c.rst)
	}
	if safe := report.SafeTotal(all); safe > 0 {
		fmt.Fprintf(w, " · up to %s%s%s marked safe to remove", c.grn, fsx.Human(safe), c.rst)
	}
	fmt.Fprintln(w)
}

func renderResult(w io.Writer, r report.Result, c palette, opt Options) {
	dur := ""
	if opt.Verbose {
		dur = fmt.Sprintf("  %s%s%s", c.dim, r.Duration.Round(1_000_000), c.rst)
	}
	switch r.Status {
	case report.StatusOK:
		line := r.Title
		if r.Summary != "" {
			line += c.dim + "  " + r.Summary + c.rst
		}
		fmt.Fprintf(w, "  %sOK%s  %s%s\n", c.grn, c.rst, line, dur)
	case report.StatusSkipped:
		fmt.Fprintf(w, "  %s--  %s: skipped, %s%s%s\n", c.dim, r.Title, r.SkipReason, c.rst, dur)
	case report.StatusError:
		fmt.Fprintf(w, "  %sERR%s %s: %s%s\n", c.red, c.rst, r.Title, r.Error, dur)
	case report.StatusFlag:
		line := r.Title
		if r.Summary != "" {
			line += ": " + r.Summary
		}
		fmt.Fprintf(w, "  %s!!%s  %s%s\n", c.yel, c.rst, line, dur)
		for _, it := range r.Items {
			size := ""
			if it.Size > 0 {
				size = fsx.Human(it.Size)
			}
			detail := ""
			if it.Detail != "" {
				detail = "  " + c.dim + it.Detail + c.rst
			}
			fmt.Fprintf(w, "      %6s  %s%s  %s\n", size, it.Label, detail, tag(it.Confidence, c))
		}
	}
	// Notes carry context (e.g. disk usage per mount); show them for ok and
	// flagged results, and for everything when verbose.
	if r.Status == report.StatusOK || r.Status == report.StatusFlag || opt.Verbose {
		for _, n := range r.Notes {
			fmt.Fprintf(w, "      %s%s%s\n", c.dim, strings.ReplaceAll(n, "\n", "\n      "), c.rst)
		}
	}
	if r.Status == report.StatusFlag && r.Hint != "" {
		fmt.Fprintf(w, "      %s%s%s\n", c.cyan, r.Hint, c.rst)
	}
}

func tag(conf report.Confidence, c palette) string {
	switch conf {
	case report.ConfidenceSafe:
		return c.grn + "[safe]" + c.rst
	case report.ConfidenceReview:
		return c.yel + "[review]" + c.rst
	case report.ConfidenceManual:
		return c.dim + "[manual]" + c.rst
	}
	return ""
}
