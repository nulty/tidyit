package main

import (
	"fmt"
	"html"
	"os"
	"strings"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"

	"tidy/internal/app"
	"tidy/internal/fsx"
	"tidy/pkg/report"
	"tidy/render/view"
)

// itemCap bounds the rows built per check; a check can report thousands
// of items and every row is a widget.
const itemCap = 200

// buildPage lays out a finished run: the overview, flagged checks biggest
// first, then problems, checks that didn't run and checks that found
// nothing.
func buildPage(res *finished, w *window) *adw.PreferencesPage {
	o := view.Build(res.groups, app.Headline)
	page := adw.NewPreferencesPage()

	page.Add(overviewGroup(o))

	if len(o.Flagged) > 0 {
		g := adw.NewPreferencesGroup()
		g.SetTitle("Found")
		g.SetDescription("Biggest first. Tidy never removes anything: copy a command to act on an item.")
		for _, r := range o.Flagged {
			g.Add(flaggedRow(r, w))
		}
		page.Add(g)
	}

	if len(o.Errors)+len(res.warnings) > 0 {
		g := adw.NewPreferencesGroup()
		g.SetTitle("Problems")
		for _, r := range o.Errors {
			g.Add(textRow(r.Title, r.Error, "dialog-warning-symbolic"))
		}
		for _, msg := range res.warnings {
			g.Add(textRow("Plugin warning", msg, "dialog-warning-symbolic"))
		}
		page.Add(g)
	}

	if len(o.Skipped) > 0 {
		g := adw.NewPreferencesGroup()
		g.SetTitle("Didn't Run")
		for _, r := range o.Skipped {
			g.Add(textRow(r.Title, r.SkipReason, ""))
		}
		page.Add(g)
	}

	if len(o.OK) > 0 {
		g := adw.NewPreferencesGroup()
		exp := adw.NewExpanderRow()
		exp.SetTitle(esc(plural(len(o.OK), "check", "checks") + " found nothing"))
		exp.SetIconName("object-select-symbolic")
		for _, r := range o.OK {
			row := textRow(r.Title, r.Summary, "")
			exp.AddRow(row)
			for _, n := range r.Notes {
				exp.AddRow(noteRow(n))
			}
		}
		g.Add(exp)
		page.Add(g)
	}
	return page
}

func overviewGroup(o view.Overview) *adw.PreferencesGroup {
	g := adw.NewPreferencesGroup()
	for _, h := range o.Headlines {
		g.Add(textRow(h, "", "drive-harddisk-symbolic"))
	}
	if o.Safe > 0 {
		g.Add(sizeRow("Safe to remove", "Caches and leftovers that are regenerated or clearly unused", o.Safe, "success"))
	}
	if o.Review > 0 {
		g.Add(sizeRow("Worth reviewing", "Probably unused, but may hold data: look before removing", o.Review, "warning"))
	}
	if len(o.Flagged) == 0 && len(o.Errors) == 0 {
		g.Add(textRow("Nothing flagged", "This machine looks tidy", "object-select-symbolic"))
	}
	return g
}

// flaggedRow is one flagged check, expanding to its items.
func flaggedRow(r report.Result, w *window) *adw.ExpanderRow {
	exp := adw.NewExpanderRow()
	exp.SetTitle(esc(r.Title))
	exp.SetSubtitle(esc(checkSubtitle(r)))
	exp.SetSubtitleLines(1)

	shared := sharedConfidence(r.Items)
	if shared != "" {
		exp.AddSuffix(confLabel(shared))
	} else {
		exp.AddSuffix(confLabel(r.Caution()))
	}
	if n := r.TotalSize(); n > 0 {
		exp.AddSuffix(sizeLabel(n))
	}

	if r.Summary != "" {
		exp.AddRow(captionRow(r.Summary))
	}
	for _, n := range r.Notes {
		exp.AddRow(noteRow(n))
	}
	items := r.Items
	if len(items) > itemCap {
		items = items[:itemCap]
	}
	for _, it := range items {
		exp.AddRow(itemRow(it, shared == "", w))
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
		exp.AddRow(textRow(more, "tidy -v --check "+r.ID+" lists them all", ""))
	}
	if r.Hint != "" {
		exp.AddRow(textRow(r.Hint, "", "dialog-information-symbolic"))
	}
	return exp
}

func itemRow(it report.Item, showConf bool, w *window) *adw.ActionRow {
	row := adw.NewActionRow()
	row.SetTitle(esc(it.Label))
	row.SetTitleLines(2)
	if sub := itemSubtitle(it); sub != "" {
		row.SetSubtitle(sub)
		row.SetSubtitleLines(4)
	}
	if showConf {
		row.AddSuffix(confLabel(it.Confidence))
	}
	if it.Size > 0 {
		row.AddSuffix(sizeLabel(it.Size))
	}
	if it.Path != "" {
		if _, err := os.Lstat(it.Path); err == nil {
			path := it.Path
			b := flatButton("folder-open-symbolic", "Show in Files")
			b.ConnectClicked(func() { w.showInFiles(path) })
			row.AddSuffix(b)
		}
	}
	if it.Action != nil && it.Action.Command != "" {
		cmd := it.Action.Command
		b := flatButton("edit-copy-symbolic", "Copy command: "+cmd)
		b.ConnectClicked(func() { w.copy(cmd, "Command") })
		row.AddSuffix(b)
	}
	return row
}

// itemSubtitle is the item's detail, its path when the label doesn't
// already show it, and its command, as Pango markup.
func itemSubtitle(it report.Item) string {
	var lines []string
	if it.Detail != "" {
		lines = append(lines, esc(it.Detail))
	}
	if it.Path != "" && !strings.Contains(it.Label, it.Path) && !strings.Contains(it.Detail, it.Path) {
		lines = append(lines, esc(it.Path))
	}
	if it.Action != nil && it.Action.Command != "" {
		lines = append(lines, "<tt>"+esc(it.Action.Command)+"</tt>")
	}
	return strings.Join(lines, "\n")
}

// checkSubtitle is a flagged check's count and its first few item labels.
func checkSubtitle(r report.Result) string {
	s := plural(len(r.Items), "item", "items")
	const preview = 3
	var names []string
	for i, it := range r.Items {
		if i == preview {
			break
		}
		names = append(names, it.Label)
	}
	if len(names) > 0 {
		s += ": " + strings.Join(names, ", ")
		if len(r.Items) > preview {
			s += ", …"
		}
	}
	return s
}

func textRow(title, subtitle, icon string) *adw.ActionRow {
	row := adw.NewActionRow()
	row.SetTitle(esc(title))
	row.SetTitleLines(0) // wrap, never truncate
	if subtitle != "" {
		row.SetSubtitle(esc(subtitle))
		row.SetSubtitleSelectable(true)
	}
	if icon != "" {
		row.SetIconName(icon)
	}
	return row
}

func sizeRow(title, subtitle string, n int64, class string) *adw.ActionRow {
	row := textRow(title, subtitle, "")
	l := sizeLabel(n)
	l.AddCSSClass("title-3")
	l.AddCSSClass(class)
	row.AddSuffix(l)
	return row
}

// noteRow shows a note verbatim; notes are often aligned columns.
func noteRow(text string) *adw.PreferencesRow {
	row, l := labelRow(text)
	l.AddCSSClass("monospace")
	l.AddCSSClass("caption")
	return row
}

// captionRow shows a line of prose, such as a check's summary.
func captionRow(text string) *adw.PreferencesRow {
	row, l := labelRow(text)
	l.AddCSSClass("caption")
	return row
}

func labelRow(text string) (*adw.PreferencesRow, *gtk.Label) {
	l := gtk.NewLabel(strings.TrimRight(text, "\n"))
	l.SetXAlign(0)
	l.SetWrap(true)
	l.SetWrapMode(pango.WrapWordChar)
	l.SetSelectable(true)
	l.AddCSSClass("dim-label")
	l.SetMarginTop(8)
	l.SetMarginBottom(8)
	l.SetMarginStart(12)
	l.SetMarginEnd(12)
	row := adw.NewPreferencesRow()
	row.SetActivatable(false)
	row.SetChild(l)
	return row, l
}

func sizeLabel(n int64) *gtk.Label {
	l := gtk.NewLabel(fsx.Human(n))
	l.AddCSSClass("numeric")
	l.SetVAlign(gtk.AlignCenter)
	return l
}

// confLabel tags how safe removal is, coloured like the other reports.
func confLabel(c report.Confidence) *gtk.Label {
	l := gtk.NewLabel(string(c))
	l.AddCSSClass("caption-heading")
	l.SetVAlign(gtk.AlignCenter)
	switch c {
	case report.ConfidenceSafe:
		l.AddCSSClass("success")
		l.SetTooltipText("Safe: regenerated or clearly unused")
	case report.ConfidenceReview:
		l.AddCSSClass("warning")
		l.SetTooltipText("Review: probably unused, but may hold data")
	default:
		l.AddCSSClass("dim-label")
		l.SetTooltipText("Manual: needs you to decide")
	}
	return l
}

func flatButton(icon, tooltip string) *gtk.Button {
	b := gtk.NewButtonFromIconName(icon)
	b.SetTooltipText(tooltip)
	b.AddCSSClass("flat")
	b.SetVAlign(gtk.AlignCenter)
	return b
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

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// esc makes text safe for Pango markup, which libadwaita rows parse.
// Pango accepts the numeric entities html.EscapeString produces.
func esc(s string) string { return html.EscapeString(s) }
