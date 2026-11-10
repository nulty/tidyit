package main

import (
	"fmt"
	"html"
	"os"
	"strings"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"

	"tidyit/internal/fsx"
	"tidyit/pkg/report"
)

// itemCap bounds the rows built per check; a check can report thousands
// of items and every row is a widget.
const itemCap = 200

// biggestFinds is how many checks the overview lists.
const biggestFinds = 6

// ---- Sidebar ----

// buildSidebar lists the disk card (the overview), the flagged checks
// grouped by how safe they are to remove, and Other checks.
func buildSidebar(res *finished) *gtk.ListBox {
	o := res.overview
	lb := gtk.NewListBox()
	lb.AddCSSClass("navigation-sidebar")
	lb.SetSelectionMode(gtk.SelectionSingle)

	lb.Append(diskCardRow(res))

	for _, g := range groupByConfidence(o.Flagged) {
		var total int64
		cls := "dim-label"
		switch g.Conf {
		case report.ConfidenceSafe:
			total, cls = o.Safe, "success"
		case report.ConfidenceReview:
			total, cls = o.Review, "warning"
		}
		lb.Append(headingRow(g.Name, total, cls))
		for _, r := range g.Results {
			size := ""
			if n := r.TotalSize(); n > 0 {
				size = fsx.Human(n)
			}
			lb.Append(sidebarRow(r.ID, checkIcon(r), r.Title, size, false))
		}
	}

	// A plain rule: a GtkSeparator in a navigation sidebar row is drawn
	// as a full-height block.
	sep := gtk.NewListBoxRow()
	sep.SetSelectable(false)
	sep.SetActivatable(false)
	sep.SetCanFocus(false)
	sep.AddCSSClass("sidebar-rule-row")
	rule := gtk.NewBox(gtk.OrientationHorizontal, 0)
	rule.AddCSSClass("sidebar-rule")
	sep.SetChild(rule)
	lb.Append(sep)

	n := len(o.OK) + len(o.Skipped) + res.problems()
	lb.Append(sidebarRow(keyOther, "object-select-symbolic", "Other checks", fmt.Sprint(n), res.problems() > 0))
	return lb
}

// diskCardRow is the overview's entry: the main filesystem's usage and
// the reclaimable totals.
func diskCardRow(res *finished) *gtk.ListBoxRow {
	box := gtk.NewBox(gtk.OrientationVertical, 0)
	box.SetMarginTop(10)
	box.SetMarginBottom(10)
	box.SetMarginStart(4)
	box.SetMarginEnd(4)

	top := gtk.NewBox(gtk.OrientationHorizontal, 10)
	top.Append(gtk.NewImageFromIconName("drive-harddisk-symbolic"))
	disk := res.disk
	name := gtk.NewLabel("Overview")
	name.AddCSSClass("heading")
	name.SetHExpand(true)
	name.SetXAlign(0)
	top.Append(name)
	box.Append(top)

	if disk != nil {
		name.SetText(disk.Mount)
		pct := gtk.NewLabel(fmt.Sprintf("%d%% full", disk.Percent))
		pct.AddCSSClass("heading")
		pct.AddCSSClass("numeric")
		top.Append(pct)
		bar := diskBar(disk, "thin")
		bar.SetMarginTop(10)
		bar.SetMarginBottom(8)
		box.Append(bar)
	}

	var parts []string
	if disk != nil {
		parts = append(parts, esc(disk.Free)+" free")
	}
	o := res.overview
	if o.Safe > 0 {
		parts = append(parts, `<span weight="bold" foreground="`+successColor+`">`+fsx.Human(o.Safe)+" safe</span>")
	}
	if o.Review > 0 {
		parts = append(parts, `<span weight="bold" foreground="`+warningColor+`">`+fsx.Human(o.Review)+" to review</span>")
	}
	if len(parts) > 0 {
		l := gtk.NewLabel("")
		l.SetMarkup(strings.Join(parts, " · "))
		l.SetXAlign(0)
		l.SetWrap(true)
		l.AddCSSClass("caption")
		if disk == nil {
			l.SetMarginTop(6)
		}
		box.Append(l)
	}

	row := gtk.NewListBoxRow()
	row.SetName(keyOverview)
	row.AddCSSClass("disk-card")
	row.SetChild(box)
	row.SetTooltipText("Overview")
	return row
}

// Pango markup can't use CSS named colours. These are libadwaita's
// success and warning colours at a lightness that reads on both the
// light and the dark sidebar; the size labels are bold, so 3:1 suffices.
const (
	successColor = "#2ec27e"
	warningColor = "#e5a50a"
)

func diskBar(d *mountUsage, size string) *gtk.LevelBar {
	bar := gtk.NewLevelBar()
	for _, off := range []string{"low", "high", "full"} {
		bar.RemoveOffsetValue(off)
	}
	bar.SetValue(float64(d.Percent) / 100)
	bar.AddCSSClass("disk")
	bar.AddCSSClass(size)
	if d.Flagged {
		bar.AddCSSClass("full")
	}
	return bar
}

func headingRow(name string, total int64, cls string) *gtk.ListBoxRow {
	box := gtk.NewBox(gtk.OrientationHorizontal, 6)
	box.AddCSSClass("sidebar-heading")
	l := gtk.NewLabel(name)
	l.SetXAlign(0)
	l.SetHExpand(true)
	l.AddCSSClass("dim-label")
	box.Append(l)
	if total > 0 {
		t := gtk.NewLabel(fsx.Human(total))
		t.AddCSSClass("numeric")
		t.AddCSSClass(cls)
		box.Append(t)
	}
	row := gtk.NewListBoxRow()
	row.SetSelectable(false)
	row.SetActivatable(false)
	row.SetCanFocus(false)
	row.SetChild(box)
	return row
}

func sidebarRow(key, icon, title, suffix string, warn bool) *gtk.ListBoxRow {
	box := gtk.NewBox(gtk.OrientationHorizontal, 10)
	box.Append(gtk.NewImageFromIconName(icon))
	l := gtk.NewLabel(title)
	l.SetXAlign(0)
	l.SetHExpand(true)
	l.SetEllipsize(pango.EllipsizeEnd)
	box.Append(l)
	if warn {
		img := gtk.NewImageFromIconName("dialog-warning-symbolic")
		img.AddCSSClass("warning")
		img.SetTooltipText("Some checks had problems")
		box.Append(img)
	}
	if suffix != "" {
		s := gtk.NewLabel(suffix)
		s.AddCSSClass("dim-label")
		s.AddCSSClass("numeric")
		box.Append(s)
	}
	row := gtk.NewListBoxRow()
	row.SetName(key)
	row.SetChild(box)
	row.SetTooltipText(title)
	return row
}

// ---- Pages ----

// pageBox is the scrolling, width-clamped column every page is built in.
func pageBox(maxWidth int) (*gtk.ScrolledWindow, *gtk.Box) {
	box := gtk.NewBox(gtk.OrientationVertical, 24)
	box.SetMarginTop(28)
	box.SetMarginBottom(40)
	box.SetMarginStart(16)
	box.SetMarginEnd(16)
	clamp := adw.NewClamp()
	clamp.SetMaximumSize(maxWidth)
	clamp.SetTighteningThreshold(maxWidth * 3 / 4)
	clamp.SetChild(box)
	sw := gtk.NewScrolledWindow()
	sw.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)
	sw.SetChild(clamp)
	return sw, box
}

// overviewPage shows the disk, the reclaimable totals and the biggest
// finds.
func overviewPage(res *finished, w *window) *gtk.ScrolledWindow {
	o := res.overview
	sw, box := pageBox(680)

	if d := res.disk; d != nil {
		card := gtk.NewBox(gtk.OrientationVertical, 0)
		card.AddCSSClass("card")
		card.AddCSSClass("overview-card")
		top := gtk.NewBox(gtk.OrientationHorizontal, 10)
		mount := gtk.NewLabel(d.Mount)
		mount.AddCSSClass("title-2")
		top.Append(mount)
		pct := gtk.NewLabel(fmt.Sprintf("%d%% full", d.Percent))
		pct.AddCSSClass("title-2")
		pct.AddCSSClass("numeric")
		if d.Flagged {
			pct.AddCSSClass("warning")
		}
		pct.SetHExpand(true)
		pct.SetXAlign(0)
		top.Append(pct)
		free := gtk.NewLabel(d.Free + " free")
		free.AddCSSClass("dim-label")
		top.Append(free)
		card.Append(top)
		bar := diskBar(d, "thick")
		bar.SetMarginTop(14)
		bar.SetMarginBottom(6)
		card.Append(bar)
		var notes []string
		if d.Flagged && res.diskSummary != "" {
			notes = append(notes, sentence(res.diskSummary)+", so Tidy It flags it")
		}
		for _, m := range res.otherMounts {
			notes = append(notes, fmt.Sprintf("Also %s: %d%% full, %s free", m.Mount, m.Percent, m.Free))
		}
		if len(notes) > 0 {
			l := wrapLabel(strings.Join(notes, "\n"))
			l.AddCSSClass("caption")
			l.AddCSSClass("dim-label")
			card.Append(l)
		}
		box.Append(card)
	} else {
		for _, h := range o.Headlines {
			box.Append(wrapLabel(h))
		}
	}

	tiles := gtk.NewFlowBox()
	tiles.SetSelectionMode(gtk.SelectionNone)
	tiles.SetHomogeneous(true)
	tiles.SetMinChildrenPerLine(1)
	tiles.SetMaxChildrenPerLine(3)
	tiles.SetColumnSpacing(12)
	tiles.SetRowSpacing(12)
	tiles.Append(tile(fsx.Human(o.Safe), "success", "Safe to remove", "Regenerated or clearly unused"))
	tiles.Append(tile(fsx.Human(o.Review), "warning", "Worth reviewing", "Probably unused, may hold data"))
	tiles.Append(tile(fmt.Sprint(len(o.Flagged)), "", "Checks flagged", fmt.Sprintf("of %d that ran", ranCount(res.rep.Results))))
	for c := tiles.FirstChild(); c != nil; c = gtk.BaseWidget(c).NextSibling() {
		gtk.BaseWidget(c).SetCanFocus(false)
	}
	box.Append(tiles)

	if n := res.problems(); n > 0 {
		g := adw.NewPreferencesGroup()
		row := adw.NewActionRow()
		row.SetTitle(esc(plural(n, "problem", "problems")))
		row.SetSubtitle("Some checks failed or a plugin couldn't load")
		row.AddPrefix(warningIcon())
		row.AddSuffix(gtk.NewImageFromIconName("go-next-symbolic"))
		row.SetActivatable(true)
		row.ConnectActivated(func() { w.open(keyOther) })
		g.Add(row)
		box.Append(g)
	}

	var top []report.Result
	for _, r := range o.Flagged {
		if r.TotalSize() > 0 && len(top) < biggestFinds {
			top = append(top, r)
		}
	}
	switch {
	case len(top) > 0:
		g := adw.NewPreferencesGroup()
		g.SetTitle("Biggest finds")
		sizes := gtk.NewSizeGroup(gtk.SizeGroupHorizontal)
		for _, r := range top {
			row := adw.NewActionRow()
			row.SetTitle(esc(r.Title))
			row.AddPrefix(gtk.NewImageFromIconName(checkIcon(r)))
			row.AddSuffix(badge(r.Caution()))
			size := sizeLabel(r.TotalSize())
			size.AddCSSClass("heading")
			size.SetXAlign(1)
			sizes.AddWidget(size)
			row.AddSuffix(size)
			row.AddSuffix(gtk.NewImageFromIconName("go-next-symbolic"))
			row.SetActivatable(true)
			id := r.ID
			row.ConnectActivated(func() { w.open(id) })
			g.Add(row)
		}
		box.Append(g)
	case len(o.Flagged) == 0 && len(o.Errors) == 0:
		st := adw.NewStatusPage()
		st.SetIconName("object-select-symbolic")
		st.SetTitle("Nothing Flagged")
		st.SetDescription("This machine looks tidy")
		box.Append(st)
	}
	return sw
}

func tile(value, valueClass, title, desc string) *gtk.Box {
	b := gtk.NewBox(gtk.OrientationVertical, 2)
	b.AddCSSClass("card")
	b.AddCSSClass("tile")
	v := gtk.NewLabel(value)
	v.AddCSSClass("big-number")
	v.AddCSSClass("numeric")
	if valueClass != "" {
		v.AddCSSClass(valueClass)
	}
	v.SetXAlign(0)
	b.Append(v)
	t := gtk.NewLabel(title)
	t.AddCSSClass("heading")
	t.SetXAlign(0)
	b.Append(t)
	d := wrapLabel(desc)
	d.SetMaxWidthChars(16) // let three tiles share a line
	d.AddCSSClass("caption")
	d.AddCSSClass("dim-label")
	b.Append(d)
	return b
}

// checkPage is one flagged check: what it is, its hint, and every item
// with its command.
func checkPage(r report.Result, w *window) *gtk.ScrolledWindow {
	sw, box := pageBox(720)
	box.SetSpacing(16)

	head := gtk.NewBox(gtk.OrientationHorizontal, 14)
	icon := gtk.NewImageFromIconName(checkIcon(r))
	icon.SetPixelSize(22)
	icon.AddCSSClass("icon-tile")
	icon.SetVAlign(gtk.AlignCenter)
	head.Append(icon)
	titles := gtk.NewBox(gtk.OrientationVertical, 0)
	titles.SetHExpand(true)
	titles.SetVAlign(gtk.AlignCenter)
	t := wrapLabel(r.Title)
	t.AddCSSClass("title-2")
	titles.Append(t)
	facts := gtk.NewLabel(checkFacts(r))
	facts.AddCSSClass("dim-label")
	facts.SetXAlign(0)
	titles.Append(facts)
	head.Append(titles)
	head.Append(badge(r.Caution()))
	box.Append(head)

	if r.Hint != "" {
		hint := gtk.NewBox(gtk.OrientationHorizontal, 10)
		hint.AddCSSClass("hint-banner")
		i := gtk.NewImageFromIconName("dialog-information-symbolic")
		i.SetVAlign(gtk.AlignStart)
		hint.Append(i)
		l := wrapLabel(sentence(r.Hint))
		// Selectable for copying, but out of the focus chain: a focused
		// selectable label selects all its text when the page opens.
		l.SetSelectable(true)
		l.SetCanFocus(false)
		l.SetHExpand(true)
		hint.Append(l)
		box.Append(hint)
	}

	cmds := commands(r)
	if len(cmds) > 1 || r.Summary != "" {
		bar := gtk.NewBox(gtk.OrientationHorizontal, 12)
		if len(cmds) > 1 {
			b := gtk.NewButton()
			content := adw.NewButtonContent()
			content.SetIconName("edit-copy-symbolic")
			content.SetLabel("Copy All Commands")
			b.SetChild(content)
			b.AddCSSClass("round-button")
			b.SetVAlign(gtk.AlignCenter)
			b.SetTooltipText(fmt.Sprintf("Copy %d commands, one per line", len(cmds)))
			all := strings.Join(cmds, "\n") + "\n"
			n := len(cmds)
			b.ConnectClicked(func() { w.copy(all, plural(n, "command", "commands")) })
			bar.Append(b)
		}
		if r.Summary != "" {
			l := wrapLabel(sentence(r.Summary))
			l.AddCSSClass("dim-label")
			l.SetHExpand(true)
			bar.Append(l)
		}
		box.Append(bar)
	}

	list := gtk.NewListBox()
	list.AddCSSClass("boxed-list")
	list.SetSelectionMode(gtk.SelectionNone)
	perItem := mixed(r.Items)
	sizes := gtk.NewSizeGroup(gtk.SizeGroupHorizontal)
	buttons := gtk.NewSizeGroup(gtk.SizeGroupHorizontal)
	items := r.Items[:min(len(r.Items), itemCap)]
	for _, it := range items {
		list.Append(itemRow(it, perItem, sizes, buttons, w))
	}
	if rest := r.Items[len(items):]; len(rest) > 0 {
		var size int64
		for _, it := range rest {
			size += it.Size
		}
		title := fmt.Sprintf("%d more not shown", len(rest))
		if size > 0 {
			title += " (" + fsx.Human(size) + ")"
		}
		row := adw.NewActionRow()
		row.SetTitle(esc(title))
		row.SetSubtitle(esc("tidyit -v --check " + r.ID + " lists them all"))
		list.Append(row)
	}
	box.Append(list)

	if len(r.Notes) > 0 {
		box.Append(notesLabel(r.Notes))
	}
	foot := wrapLabel("Hover a copy button to see its command. Tidy It never runs anything.")
	foot.AddCSSClass("caption")
	foot.AddCSSClass("dim-label")
	box.Append(foot)
	return sw
}

func itemRow(it report.Item, showConf bool, sizes, buttons *gtk.SizeGroup, w *window) *adw.ActionRow {
	row := adw.NewActionRow()
	row.SetTitle(esc(it.Label))
	row.SetTitleLines(2)
	if sub := itemSubtitle(it); sub != "" {
		row.SetSubtitle(esc(sub))
		row.SetSubtitleLines(2)
	}
	if showConf {
		row.AddSuffix(badge(it.Confidence))
	}
	size := gtk.NewLabel("")
	if it.Size > 0 {
		size.SetText(fsx.Human(it.Size))
	}
	size.AddCSSClass("numeric")
	size.SetXAlign(1)
	sizes.AddWidget(size)
	row.AddSuffix(size)

	acts := gtk.NewBox(gtk.OrientationHorizontal, 2)
	acts.SetHAlign(gtk.AlignEnd)
	acts.SetVAlign(gtk.AlignCenter)
	if it.Path != "" {
		if _, err := os.Lstat(it.Path); err == nil {
			path := it.Path
			b := flatButton("folder-open-symbolic", "Show in Files")
			b.ConnectClicked(func() { w.showInFiles(path) })
			acts.Append(b)
		}
	}
	if it.Action != nil && it.Action.Command != "" {
		cmd := it.Action.Command
		b := flatButton("edit-copy-symbolic", cmd)
		b.ConnectClicked(func() { w.copy(cmd, "Command") })
		acts.Append(b)
	}
	// Reserve room for both buttons, right-aligned, so sizes line up
	// down the list.
	slot := gtk.NewBox(gtk.OrientationHorizontal, 0)
	slot.SetSizeRequest(2*34+2, -1)
	acts.SetHExpand(true)
	slot.SetHExpand(false) // keep acts' expand from spreading up the row
	slot.Append(acts)
	buttons.AddWidget(slot)
	row.AddSuffix(slot)
	return row
}

// otherPage lists problems, checks that didn't run and checks that found
// nothing.
func otherPage(res *finished) *gtk.ScrolledWindow {
	o := res.overview
	sw, box := pageBox(680)
	box.SetSpacing(22)

	if res.problems() > 0 {
		g := adw.NewPreferencesGroup()
		g.SetTitle("Problems")
		for _, r := range o.Errors {
			g.Add(textRow(r.Title, sentence(r.Error), warningIcon()))
		}
		for _, msg := range res.warnings {
			g.Add(textRow("Plugin warning", msg, warningIcon()))
		}
		box.Append(g)
	}

	if len(o.Skipped) > 0 {
		g := adw.NewPreferencesGroup()
		g.SetTitle("Didn't Run")
		for _, r := range o.Skipped {
			g.Add(textRow(r.Title, sentence(r.SkipReason), gtk.NewImageFromIconName(checkIcon(r))))
		}
		box.Append(g)
	}

	if len(o.OK) > 0 {
		g := adw.NewPreferencesGroup()
		g.SetTitle("Found Nothing")
		for _, r := range o.OK {
			ok := gtk.NewImageFromIconName("object-select-symbolic")
			ok.AddCSSClass("success")
			row := textRow(r.Title, "", ok)
			if r.Summary != "" {
				s := gtk.NewLabel(r.Summary)
				s.AddCSSClass("dim-label")
				row.AddSuffix(s)
			}
			if len(r.Notes) > 0 {
				// Notes are often aligned columns: keep them monospace.
				row.SetSubtitle("<tt>" + esc(strings.TrimRight(strings.Join(r.Notes, "\n"), "\n")) + "</tt>")
				row.SetSubtitleLines(0)
				row.SetSubtitleSelectable(true)
			}
			g.Add(row)
		}
		box.Append(g)
	}
	return sw
}

// ---- Small widgets ----

func textRow(title, subtitle string, prefix gtk.Widgetter) *adw.ActionRow {
	row := adw.NewActionRow()
	row.SetTitle(esc(title))
	row.SetTitleLines(0) // wrap, never truncate
	if subtitle != "" {
		row.SetSubtitle(esc(subtitle))
		row.SetSubtitleSelectable(true)
	}
	if prefix != nil {
		row.AddPrefix(prefix)
	}
	return row
}

func notesLabel(notes []string) *gtk.Label {
	l := gtk.NewLabel(strings.TrimRight(strings.Join(notes, "\n"), "\n"))
	l.SetXAlign(0)
	l.SetWrap(true)
	l.SetWrapMode(pango.WrapWordChar)
	l.SetSelectable(true)
	l.AddCSSClass("monospace")
	l.AddCSSClass("caption")
	l.AddCSSClass("dim-label")
	return l
}

func wrapLabel(text string) *gtk.Label {
	l := gtk.NewLabel(text)
	l.SetXAlign(0)
	l.SetWrap(true)
	l.SetWrapMode(pango.WrapWordChar)
	return l
}

func warningIcon() *gtk.Image {
	img := gtk.NewImageFromIconName("dialog-warning-symbolic")
	img.AddCSSClass("warning")
	return img
}

func sizeLabel(n int64) *gtk.Label {
	l := gtk.NewLabel(fsx.Human(n))
	l.AddCSSClass("numeric")
	l.SetVAlign(gtk.AlignCenter)
	return l
}

// badge tags how safe removal is, coloured like the other reports.
func badge(c report.Confidence) *gtk.Label {
	l := gtk.NewLabel(string(c))
	l.AddCSSClass("badge")
	l.SetVAlign(gtk.AlignCenter)
	switch c {
	case report.ConfidenceSafe:
		l.AddCSSClass("success")
		l.SetTooltipText("Safe: regenerated or clearly unused")
	case report.ConfidenceReview:
		l.AddCSSClass("warning")
		l.SetTooltipText("Review: probably unused, but may hold data")
	default:
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

// esc makes text safe for Pango markup, which libadwaita rows parse.
// Pango accepts the numeric entities html.EscapeString produces.
func esc(s string) string { return html.EscapeString(s) }
