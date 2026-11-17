package main

import (
	"context"
	"fmt"
	"math"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"tidyit/internal/app"
	"tidyit/internal/config"
	"tidyit/internal/engine"
	"tidyit/pkg/manifest"
)

// settings is the Preferences dialog. Each page lists checks by plugin,
// with an on/off switch; a check's row opens a subpage with its options.
// Every change is written to config.toml straight away, so there is no
// Apply button; the results only change when the user checks again.
type settings struct {
	*adw.PreferencesDialog
	w       *window
	path    string
	insts   []*engine.Instance
	rows    map[string]*adw.ActionRow // check id → its row on a page
	toggles map[string]*gtk.Switch    // check id → the row's switch
	syncing bool                      // set while widgets are updated from code
	changed bool                      // something was saved
}

// showSettings opens Preferences, at a check's subpage when checkID is set.
// It returns nil if the config file can't be read.
func (w *window) showSettings(checkID string) *settings {
	path := config.DefaultPath()
	cfg, err := app.LoadConfig("")
	if err != nil {
		w.configBroken(path, err)
		return nil
	}
	reg, _ := app.Registry()
	insts, _ := reg.Instances(cfg)

	s := &settings{PreferencesDialog: adw.NewPreferencesDialog(), w: w, path: path, insts: insts,
		rows: map[string]*adw.ActionRow{}, toggles: map[string]*gtk.Switch{}}
	s.SetSearchEnabled(true)
	for _, p := range settingsPages(insts, lookPath) {
		s.Add(s.page(p))
	}
	s.ConnectClosed(func() {
		if s.changed {
			w.settingsBanner.SetRevealed(true)
		}
	})
	s.Present(w)
	if checkID == "" {
		return s
	}
	if page, ok := pageOf(insts, checkID); ok {
		s.SetVisiblePageName(string(page))
		for _, in := range insts {
			if in.ID == checkID {
				s.openCheck(in)
			}
		}
	}
	return s
}

// configBroken explains that settings can't be changed until the config
// file parses, rather than overwriting a file it couldn't read.
func (w *window) configBroken(path string, err error) {
	d := adw.NewAlertDialog("Settings File Has an Error",
		fmt.Sprintf("%s\n\nFix it in a text editor, then open Preferences again. Tidy It won't overwrite a file it can't read.", err))
	d.AddResponse("close", "Close")
	d.AddResponse("open", "Open File")
	d.SetResponseAppearance("open", adw.ResponseSuggested)
	d.ConnectResponse(func(resp string) {
		if resp != "open" {
			return
		}
		l := gtk.NewFileLauncher(gio.NewFileForPath(path))
		l.Launch(context.Background(), &w.Window, func(res gio.AsyncResulter) {
			if err := l.LaunchFinish(res); err != nil && !isDismissed(err) {
				w.toast("Can't open the file: " + err.Error())
			}
		})
	})
	d.Present(w)
}

func (s *settings) page(p settingsPage) *adw.PreferencesPage {
	page := adw.NewPreferencesPage()
	page.SetName(string(p.Category))
	page.SetTitle(p.Title)
	page.SetIconName(p.Icon)
	for _, g := range p.Groups {
		group := adw.NewPreferencesGroup()
		group.SetTitle(esc(g.Title))
		if text := missingText(g.Missing); text != "" {
			group.SetDescription(esc(sentence(text)))
		}
		for _, in := range g.Insts {
			group.Add(s.checkRow(in))
		}
		page.Add(group)
	}
	return page
}

// checkRow is a check on a page: its title, what was changed, a switch,
// and a chevron to its subpage.
func (s *settings) checkRow(in *engine.Instance) *adw.ActionRow {
	row := adw.NewActionRow()
	row.SetTitle(esc(in.Title))
	row.SetSubtitleLines(2)
	sw := gtk.NewSwitch()
	sw.SetVAlign(gtk.AlignCenter)
	sw.SetActive(in.Enabled)
	sw.SetTooltipText("Run this check")
	sw.NotifyProperty("active", func() {
		if !s.syncing {
			s.setEnabled(in, sw.Active())
		}
	})
	row.AddSuffix(sw)
	row.AddSuffix(gtk.NewImageFromIconName("go-next-symbolic"))
	row.SetActivatable(true)
	row.ConnectActivated(func() { s.openCheck(in) })
	s.rows[in.ID], s.toggles[in.ID] = row, sw
	s.updateRow(in)
	return row
}

func (s *settings) updateRow(in *engine.Instance) {
	row := s.rows[in.ID]
	if row == nil {
		return
	}
	sub := checkSubtitle(in)
	if in.OptErr != nil {
		sub = "Settings error: " + in.OptErr.Error()
	}
	row.SetSubtitle(esc(sub))
	s.syncing = true
	s.toggles[in.ID].SetActive(in.Enabled)
	s.syncing = false
}

// save applies change to the config file and reports whether it worked.
func (s *settings) save(change func(*config.Config) error) bool {
	backup, err := config.Update(s.path, change)
	if err != nil {
		s.toast("Not saved: " + err.Error())
		return false
	}
	if backup != "" {
		s.toast("Your hand-written settings were kept in " + tildePath(backup, homeDir))
	}
	s.changed = true
	return true
}

func (s *settings) toast(msg string) {
	t := adw.NewToast(esc(msg))
	t.SetTimeout(5)
	s.AddToast(t)
}

func (s *settings) setEnabled(in *engine.Instance, on bool) bool {
	ok := s.save(func(c *config.Config) error {
		c.SetEnabled(in.ID, enabledOverride(c, in, on))
		return nil
	})
	if ok {
		in.Enabled = on
	}
	s.updateRow(in)
	return ok
}

// setOption saves one option, after checking it against the schema.
func (s *settings) setOption(in *engine.Instance, name string, v any) bool {
	ok := s.save(func(c *config.Config) error {
		c.SetOption(in.ID, name, override(in, name, v))
		return validate(c, in)
	})
	if ok {
		in.Options[name] = v
		in.OptErr = nil
	}
	s.updateRow(in)
	return ok
}

// reset removes every override for a check.
func (s *settings) reset(in *engine.Instance) bool {
	var enabled bool
	ok := s.save(func(c *config.Config) error {
		c.Reset(in.ID)
		enabled = in.DefaultEnabled
		if inh := c.Inherited(in.ID); inh != nil {
			enabled = *inh
		}
		return nil
	})
	if ok {
		in.Options = manifest.Values{}
		for k, v := range in.Defaults {
			in.Options[k] = v
		}
		in.OptErr = nil
		in.Enabled = enabled
	}
	s.updateRow(in)
	return ok
}

// ---- A check's subpage ----

// openCheck pushes the subpage with a check's switch and options.
func (s *settings) openCheck(in *engine.Instance) {
	view := adw.NewToolbarView()
	view.AddTopBar(adw.NewHeaderBar())
	var refresh func()
	refresh = func() { view.SetContent(s.checkPage(in, refresh)) }
	refresh()
	s.PushSubpage(adw.NewNavigationPage(view, in.Title))
}

// checkPage builds a check's subpage; refresh rebuilds it after a reset
// or a failed save, so every widget shows the saved value.
func (s *settings) checkPage(in *engine.Instance, refresh func()) *adw.PreferencesPage {
	page := adw.NewPreferencesPage()

	// The header bar already shows the check's title.
	top := adw.NewPreferencesGroup()
	if d := checkDescription(in); d != "" {
		top.SetDescription(esc(d))
	}
	run := adw.NewSwitchRow()
	run.SetTitle("Run This Check")
	if in.Spec.DefaultOff {
		run.SetSubtitle("Off unless turned on here")
	}
	run.SetActive(in.Enabled)
	run.NotifyProperty("active", func() {
		if !s.setEnabled(in, run.Active()) {
			refresh()
		}
	})
	top.Add(run)
	page.Add(top)

	if in.OptErr != nil {
		g := adw.NewPreferencesGroup()
		g.SetDescription(esc("These settings have an error, so the check can't run: " + in.OptErr.Error()))
		page.Add(g)
	}

	names := optionNames(in.Spec)
	if len(names) == 0 {
		return page
	}
	opts := adw.NewPreferencesGroup()
	opts.SetTitle("Options")
	resetAll := gtk.NewButtonWithLabel("Reset to Defaults")
	resetAll.AddCSSClass("flat")
	resetAll.SetVAlign(gtk.AlignCenter)
	resetAll.ConnectClicked(func() {
		s.reset(in)
		refresh()
	})
	if canReset(in) {
		opts.SetHeaderSuffix(resetAll)
	}

	// After each change: show reset buttons only where something changed.
	var updaters []func()
	update := func() {
		resetAll.SetSensitive(len(changedOptions(in)) > 0 || in.Enabled != in.DefaultEnabled)
		for _, u := range updaters {
			u()
		}
	}
	for _, name := range names {
		row, undo := s.optionRow(in, name, refresh, update)
		opts.Add(row)
		if undo != nil {
			updaters = append(updaters, func() { undo.SetVisible(canReset(in) && isChanged(in, name)) })
		}
	}
	run.NotifyProperty("active", update)
	update()
	page.Add(opts)
	return page
}

// suffixer is a row that takes suffix widgets.
type suffixer interface{ AddSuffix(gtk.Widgetter) }

// undoButton resets one option to its default.
func (s *settings) undoButton(row suffixer, in *engine.Instance, name string, refresh func()) *gtk.Button {
	b := gtk.NewButtonFromIconName("edit-undo-symbolic")
	b.AddCSSClass("flat")
	b.SetVAlign(gtk.AlignCenter)
	b.SetTooltipText("Reset to Default")
	b.ConnectClicked(func() {
		s.setOption(in, name, in.Defaults[name])
		refresh()
	})
	row.AddSuffix(b)
	return b
}

// optionRow builds the widget for one option, by type. It returns the
// row and its reset button.
func (s *settings) optionRow(in *engine.Instance, name string, refresh, update func()) (gtk.Widgetter, *gtk.Button) {
	o := in.Spec.Options[name]
	label := esc(optionLabel(o, name))
	desc := esc(o.Description)
	// changed saves a new value, or puts the saved one back if it fails.
	changed := func(v any) {
		if s.setOption(in, name, v) {
			update()
		} else {
			refresh()
		}
	}

	switch {
	case o.Type == manifest.TypeBoolean:
		row := adw.NewSwitchRow()
		row.SetTitle(label)
		row.SetSubtitle(desc)
		row.SetActive(in.Options.Bool(name))
		undo := s.undoButton(row, in, name, refresh)
		row.NotifyProperty("active", func() { changed(row.Active()) })
		return row, undo

	case o.Type == manifest.TypeInteger || o.Type == manifest.TypeNumber:
		lo, hi := -1e9, 1e9
		if o.Min != nil {
			lo = *o.Min
		}
		if o.Max != nil {
			hi = *o.Max
		}
		var digits uint
		value := float64(in.Options.Int(name))
		if o.Type == manifest.TypeNumber {
			value = in.Options.Float(name)
			digits = numberDigits(value)
		}
		row := adw.NewSpinRow(gtk.NewAdjustment(value, lo, hi, 1, 10, 0), 1, digits)
		row.SetTitle(label)
		row.SetSubtitle(desc)
		undo := s.undoButton(row, in, name, refresh)
		row.NotifyProperty("value", func() {
			if o.Type == manifest.TypeInteger {
				changed(int(math.Round(row.Value())))
			} else {
				changed(row.Value())
			}
		})
		return row, undo

	case o.Type == manifest.TypeString && len(o.Enum) > 0:
		row := adw.NewComboRow()
		row.SetTitle(label)
		row.SetSubtitle(desc)
		row.SetModel(gtk.NewStringList(o.Enum))
		for i, e := range o.Enum {
			if e == in.Options.String(name) {
				row.SetSelected(uint(i))
			}
		}
		undo := s.undoButton(row, in, name, refresh)
		row.NotifyProperty("selected", func() {
			if i := int(row.Selected()); i < len(o.Enum) {
				changed(o.Enum[i])
			}
		})
		return row, undo

	case o.Type == manifest.TypeString:
		row := adw.NewEntryRow()
		row.SetTitle(label)
		row.SetTooltipText(o.Description)
		row.SetText(in.Options.String(name))
		row.SetShowApplyButton(true)
		undo := s.undoButton(row, in, name, refresh)
		row.ConnectApply(func() { changed(row.Text()) })
		return row, undo

	case o.Type == manifest.TypeStrings:
		return s.listRow(in, name, label, desc, changed, refresh)
	}
	return adw.NewActionRow(), nil
}

// listRow edits a list of strings: an expander with one removable row per
// entry and an entry row to add one (with a folder picker for paths).
func (s *settings) listRow(in *engine.Instance, name, label, desc string, changed func(any), refresh func()) (gtk.Widgetter, *gtk.Button) {
	o := in.Spec.Options[name]
	exp := adw.NewExpanderRow()
	exp.SetTitle(label)
	exp.SetSubtitle(desc)
	exp.SetSubtitleLines(2)
	count := gtk.NewLabel("")
	count.AddCSSClass("dim-label")
	count.AddCSSClass("numeric")
	exp.AddSuffix(count)
	undo := s.undoButton(exp, in, name, refresh)

	add := adw.NewEntryRow()
	add.SetTitle("Add")
	switch o.Format {
	case manifest.FormatPath:
		add.SetTitle("Add a Folder")
	case manifest.FormatGlob:
		add.SetTitle("Add a Pattern, e.g. *.iso")
	}
	add.SetShowApplyButton(true)

	var rows []gtk.Widgetter
	var fill func()
	fill = func() {
		for _, r := range rows {
			exp.Remove(r)
		}
		rows = rows[:0]
		list := in.Options.Strings(name)
		count.SetLabel(fmt.Sprint(len(list)))
		for i, v := range list {
			r := adw.NewActionRow()
			r.SetTitle(esc(v))
			r.SetTitleSelectable(true)
			rm := gtk.NewButtonFromIconName("list-remove-symbolic")
			rm.AddCSSClass("flat")
			rm.SetVAlign(gtk.AlignCenter)
			rm.SetTooltipText("Remove")
			rm.ConnectClicked(func() {
				changed(removeValue(in.Options.Strings(name), i))
				fill()
			})
			r.AddSuffix(rm)
			exp.AddRow(r)
			rows = append(rows, r)
		}
		exp.Remove(add)
		exp.AddRow(add)
	}
	addValueFrom := func(v string) {
		if list, ok := addValue(in.Options.Strings(name), v); ok {
			changed(list)
			add.SetText("")
			fill()
		}
	}
	add.ConnectApply(func() { addValueFrom(add.Text()) })
	if o.Format == manifest.FormatPath {
		pick := gtk.NewButtonFromIconName("folder-open-symbolic")
		pick.AddCSSClass("flat")
		pick.SetVAlign(gtk.AlignCenter)
		pick.SetTooltipText("Choose a Folder")
		pick.ConnectClicked(func() {
			d := gtk.NewFileDialog()
			d.SetTitle("Choose a Folder")
			d.SelectFolder(context.Background(), &s.w.Window, func(res gio.AsyncResulter) {
				f, err := d.SelectFolderFinish(res)
				if err != nil {
					if !isDismissed(err) {
						s.toast("Can't use that folder: " + err.Error())
					}
					return
				}
				if p := f.Path(); p != "" {
					addValueFrom(tildePath(p, homeDir))
				}
			})
		})
		add.AddSuffix(pick)
	}
	exp.AddRow(add)
	fill()
	return exp, undo
}

// checkSettingsButton opens Preferences at a check, from its result page.
func checkSettingsButton(id string, w *window) *gtk.Button {
	b := gtk.NewButton()
	content := adw.NewButtonContent()
	content.SetIconName("emblem-system-symbolic")
	content.SetLabel("Settings")
	b.SetChild(content)
	b.AddCSSClass("round-button")
	b.SetVAlign(gtk.AlignCenter)
	b.SetTooltipText("Change what this check looks for")
	b.ConnectClicked(func() { w.showSettings(id) })
	return b
}
