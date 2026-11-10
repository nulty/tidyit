// Package term renders a report for a terminal.
package term

import (
	"fmt"
	"io"
	"strings"

	"tidyit/internal/fsx"
	"tidyit/pkg/report"
	"tidyit/render/view"
)

// Group is a titled set of results, e.g. all results of one plugin.
type Group = view.Group

// Options control the output.
type Options struct {
	Color       bool
	OnlyFlagged bool // hide ok and skipped results
	// Full lists every flagged item under its result, with its command.
	// Without it the report is a compact overview, one line per check.
	Full bool
	// Verbose implies Full, shows every item (no cap), durations, notes
	// for all results and plugin debug output.
	Verbose bool
	// Width is the terminal width used to fit compact lines; 0 = no limit.
	Width int
	// Headline lists result ids shown in the compact header rather than
	// as rows, e.g. disk usage.
	Headline []string
}

// fullItemCap is how many items --full shows per result; -v shows all.
const fullItemCap = 10

type palette struct{ bold, dim, yel, grn, red, cyan, rst string }

func newPalette(on bool) palette {
	if !on {
		return palette{}
	}
	return palette{"\033[1m", "\033[2m", "\033[33m", "\033[32m", "\033[31m", "\033[36m", "\033[0m"}
}

// Render writes the report.
func Render(w io.Writer, rep *report.Report, groups []Group, opt Options) {
	if opt.Verbose || opt.Full {
		renderFull(w, rep, groups, opt)
		return
	}
	renderCompact(w, rep, groups, opt)
}

func renderFull(w io.Writer, rep *report.Report, groups []Group, opt Options) {
	c := newPalette(opt.Color)
	fmt.Fprintf(w, "%sTidy It report%s  %s  %s%s on %s%s\n", c.bold, c.rst,
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
	if n := report.Total(all, report.ConfidenceSafe); n > 0 {
		fmt.Fprintf(w, "  ·  %s%s safe to remove%s", c.grn, fsx.Human(n), c.rst)
	}
	if n := report.Total(all, report.ConfidenceReview); n > 0 {
		fmt.Fprintf(w, " · %s to review", fsx.Human(n))
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
		facts := []string{fmt.Sprintf("%d found", len(r.Items))}
		if n := r.TotalSize(); n > 0 {
			facts = append(facts, fsx.Human(n))
		}
		if r.Summary != "" {
			facts = append(facts, r.Summary)
		}
		// One tag on the heading when every item shares it; otherwise a
		// tag per item.
		shared := sharedConfidence(r.Items)
		line := r.Title + "  " + c.dim + strings.Join(facts, " · ") + c.rst
		if shared != "" {
			line += "  " + tag(shared, c)
		}
		fmt.Fprintf(w, "  %s!!%s  %s%s\n", c.yel, c.rst, line, dur)
		items := r.Items
		if !opt.Verbose && len(items) > fullItemCap {
			items = items[:fullItemCap]
		}
		for _, it := range items {
			size := ""
			if it.Size > 0 {
				size = fsx.Human(it.Size)
			}
			detail := ""
			if it.Detail != "" {
				detail = "  " + c.dim + it.Detail + c.rst
			}
			itemTag := ""
			if shared == "" {
				itemTag = "  " + tag(it.Confidence, c)
			}
			fmt.Fprintf(w, "      %6s  %s%s%s\n", size, it.Label, detail, itemTag)
			if it.Action != nil && it.Action.Command != "" {
				fmt.Fprintf(w, "              %s$ %s%s\n", c.dim, it.Action.Command, c.rst)
			}
		}
		if rest := r.Items[len(items):]; len(rest) > 0 {
			var size int64
			for _, it := range rest {
				size += it.Size
			}
			more := fmt.Sprintf("and %d more", len(rest))
			if size > 0 {
				more += " (" + fsx.Human(size) + ")"
			}
			fmt.Fprintf(w, "      %6s  %s%s; tidyit -v --check %s lists all%s\n", "", c.dim, more, r.ID, c.rst)
		}
	}
	// Notes carry context (e.g. disk usage per mount); show them for ok and
	// flagged results, and for everything when verbose.
	if r.Status == report.StatusOK || r.Status == report.StatusFlag || opt.Verbose {
		for _, n := range r.Notes {
			fmt.Fprintf(w, "      %s%s%s\n", c.dim, strings.ReplaceAll(n, "\n", "\n      "), c.rst)
		}
	}
	if opt.Verbose && r.Debug != "" {
		fmt.Fprintf(w, "      %sdebug:\n        %s%s\n", c.dim, strings.ReplaceAll(strings.TrimRight(r.Debug, "\n"), "\n", "\n        "), c.rst)
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

// sharedConfidence returns the items' confidence if they all have the
// same one, else "".
func sharedConfidence(items []report.Item) report.Confidence {
	if len(items) == 0 {
		return ""
	}
	c := items[0].Confidence
	for _, it := range items[1:] {
		if it.Confidence != c {
			return ""
		}
	}
	return c
}
