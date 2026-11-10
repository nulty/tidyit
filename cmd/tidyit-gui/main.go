// Command tidyit-gui is the GTK4/libadwaita front end for tidyit. Like the CLI
// it is read-only: it runs the checks, shows what they found and lets you
// copy each command; it never runs one.
package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"tidyit/internal/app"
	"tidyit/internal/config"
	"tidyit/internal/engine"
	"tidyit/pkg/report"
	"tidyit/render/htmlreport"
	"tidyit/render/view"
)

const appID = "com.inulty.TidyIt"

func main() {
	a := adw.NewApplication(appID, gio.ApplicationFlagsNone)
	a.ConnectStartup(loadCSS)
	a.ConnectActivate(func() {
		if w := a.ActiveWindow(); w != nil {
			w.Present()
			return
		}
		newWindow(a).Present()
	})
	a.SetAccelsForAction("win.rescan", []string{"<Control>r", "F5"})
	a.SetAccelsForAction("win.save", []string{"<Control>s"})
	a.SetAccelsForAction("window.close", []string{"<Control>w"})
	os.Exit(a.Run(os.Args))
}

// window is one tidyit window. Every field is touched only on the GTK main
// thread; the run goroutine hands results back with glib.IdleAdd.
type window struct {
	*adw.ApplicationWindow
	toasts   *adw.ToastOverlay
	stack    *gtk.Stack // progress, results or failure
	progress *adw.StatusPage
	rescan   *gio.SimpleAction
	save     *gio.SimpleAction

	// The results: a sidebar of checks and a page for the selected one.
	split        *adw.NavigationSplitView
	sidebar      *gtk.ListBox // replaced on every run
	sidebarView  *adw.ToolbarView
	contentPage  *adw.NavigationPage
	contentView  *adw.ToolbarView
	contentTitle *adw.WindowTitle
	scroll       *gtk.ScrolledWindow // the shown page, for screenshots

	cancel  context.CancelFunc // stops the current run, nil when idle
	closing bool
	last    *finished // the last completed run
	sel     string    // the selected sidebar key, kept across runs
}

// finished is a completed run, with what the pages show worked out once.
type finished struct {
	rep         *report.Report
	groups      []view.Group
	warnings    []string
	overview    view.Overview
	disk        *mountUsage // nil if the disk check didn't run
	otherMounts []mountUsage
	diskSummary string
}

func newFinished(rep *report.Report, groups []view.Group, warnings []string) *finished {
	res := &finished{rep: rep, groups: groups, warnings: warnings, overview: view.Build(groups, app.Headline)}
	res.disk, res.otherMounts, res.diskSummary = diskUsage(rep.Results)
	return res
}

// problems counts failed checks and plugin warnings.
func (res *finished) problems() int { return len(res.overview.Errors) + len(res.warnings) }

// result returns the flagged check with this id.
func (res *finished) result(id string) (report.Result, bool) {
	for _, r := range res.overview.Flagged {
		if r.ID == id {
			return r, true
		}
	}
	return report.Result{}, false
}

func newWindow(a *adw.Application) *window {
	w := &window{ApplicationWindow: adw.NewApplicationWindow(&a.Application), sel: keyOverview}
	w.SetDefaultSize(1100, 780)
	w.SetSizeRequest(360, 300)
	w.SetTitle("Tidy It")

	w.progress = adw.NewStatusPage()
	w.progress.SetIconName("edit-find-symbolic")
	w.progress.SetTitle("Checking…")
	spinner := gtk.NewSpinner()
	spinner.SetSizeRequest(32, 32)
	spinner.Start()
	w.progress.SetChild(spinner)

	w.stack = gtk.NewStack()
	w.stack.SetTransitionType(gtk.StackTransitionTypeCrossfade)
	w.stack.AddNamed(withHeader(w.progress), "progress")
	w.stack.AddNamed(w.buildSplit(), "results")

	w.toasts = adw.NewToastOverlay()
	w.toasts.SetChild(w.stack)
	w.SetContent(w.toasts)

	// Below 600sp the sidebar and the page share the window, one at a time.
	bp := adw.NewBreakpoint(adw.BreakpointConditionParse("max-width: 600sp"))
	bp.AddSetter(w.split, "collapsed", true)
	w.AddBreakpoint(bp)

	w.rescan = gio.NewSimpleAction("rescan", nil)
	w.rescan.ConnectActivate(func(*glib.Variant) { w.run() })
	w.AddAction(w.rescan)
	w.save = gio.NewSimpleAction("save", nil)
	w.save.ConnectActivate(func(*glib.Variant) { w.saveReport() })
	w.AddAction(w.save)

	w.ConnectCloseRequest(func() bool {
		w.closing = true
		if w.cancel != nil {
			w.cancel()
		}
		return false
	})
	w.run()
	return w
}

// run starts a fresh run of every enabled check and shows its progress.
func (w *window) run() {
	if w.cancel != nil {
		return
	}
	cfg, err := app.LoadConfig("")
	if err != nil {
		w.showFailure("Can't read settings", fmt.Sprintf("%s\n\nFix or remove %s and check again.", err, config.DefaultPath()))
		return
	}
	reg, warnings := app.Registry()
	insts, iw := reg.Instances(cfg)
	warnings = append(warnings, iw...)
	total := app.Enabled(insts)

	ctx, cancel := context.WithCancel(context.Background())
	w.cancel = cancel
	w.rescan.SetEnabled(false)
	w.save.SetEnabled(false)
	w.showProgress(0, total, nil)
	w.stack.SetVisibleChildName("progress")

	runner := engine.Runner{Concurrency: cfg.Run.Concurrency, Timeout: cfg.Run.Timeout}
	host, _ := os.Hostname()
	go func() {
		running := map[string]bool{}
		done := 0
		started := time.Now()
		rep := engine.Collect(runner.Run(ctx, insts), host, func(ev report.Event) {
			// Runs on this goroutine; copy what the UI needs before
			// handing it over.
			switch ev.Type {
			case report.EventStarted:
				running[ev.ID] = true
			case report.EventFinished:
				delete(running, ev.ID)
				done++
			}
			names := sortedKeys(running)
			n := done
			glib.IdleAdd(func() { w.showProgress(n, total, names) })
		})
		rep.StartedAt = started
		app.SortResults(rep, insts)
		res := newFinished(rep, app.Groups(rep, reg), warnings)
		glib.IdleAdd(func() {
			cancel()
			w.cancel = nil
			if w.closing {
				return
			}
			w.rescan.SetEnabled(true)
			w.showResults(res)
		})
	}()
}

func (w *window) showProgress(done, total int, running []string) {
	desc := fmt.Sprintf("%d of %d checks done", done, total)
	if len(running) > 0 {
		desc += "\n" + strings.Join(running, ", ")
	}
	w.progress.SetDescription(desc)
}

// buildSplit makes the results layout: the sidebar page, with Check
// Again, and the content page, with the run's details and the menu.
func (w *window) buildSplit() *adw.NavigationSplitView {
	refresh := gtk.NewButtonFromIconName("view-refresh-symbolic")
	refresh.SetTooltipText("Check Again (Ctrl+R)")
	refresh.SetActionName("win.rescan")
	sideHeader := adw.NewHeaderBar()
	sideHeader.PackStart(refresh)
	sideHeader.SetTitleWidget(adw.NewWindowTitle("Tidy It", ""))
	w.sidebarView = adw.NewToolbarView()
	w.sidebarView.AddTopBar(sideHeader)

	menu := gio.NewMenu()
	menu.Append("Save Report as HTML…", "win.save")
	menuButton := gtk.NewMenuButton()
	menuButton.SetIconName("open-menu-symbolic")
	menuButton.SetTooltipText("Main Menu")
	menuButton.SetMenuModel(menu)
	menuButton.SetPrimary(true)
	w.contentTitle = adw.NewWindowTitle("Overview", "")
	header := adw.NewHeaderBar()
	header.SetTitleWidget(w.contentTitle)
	header.PackEnd(menuButton)
	w.contentView = adw.NewToolbarView()
	w.contentView.AddTopBar(header)

	w.split = adw.NewNavigationSplitView()
	w.split.SetMinSidebarWidth(260)
	w.split.SetMaxSidebarWidth(320)
	w.split.SetSidebar(adw.NewNavigationPage(w.sidebarView, "Tidy It"))
	w.contentPage = adw.NewNavigationPage(w.contentView, "Overview")
	w.split.SetContent(w.contentPage)
	return w.split
}

func (w *window) showResults(res *finished) {
	w.last = res
	w.save.SetEnabled(true)
	rep := res.rep
	w.contentTitle.SetSubtitle(fmt.Sprintf("%s · %s · took %s", rep.Host,
		rep.StartedAt.Format("Mon 2 Jan 15:04"), rep.FinishedAt.Sub(rep.StartedAt).Round(100*time.Millisecond)))

	w.sidebar = buildSidebar(res)
	w.sidebar.ConnectRowSelected(func(row *gtk.ListBoxRow) {
		if row != nil {
			w.showPage(row.Name())
		}
	})
	w.sidebar.ConnectRowActivated(func(*gtk.ListBoxRow) { w.split.SetShowContent(true) })
	sw := gtk.NewScrolledWindow()
	sw.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)
	sw.SetChild(w.sidebar)
	w.sidebarView.SetContent(sw)

	// Keep the selection across runs while that check is still flagged.
	if _, ok := res.result(w.sel); !ok && w.sel != keyOther {
		w.sel = keyOverview
	}
	w.selectRow(w.sel)
	w.stack.SetVisibleChildName("results")
	if dir := screenshotDir(); dir != "" {
		w.takeScreenshots(dir)
	}
}

// open shows the page for key, as if picked in the sidebar.
func (w *window) open(key string) {
	w.selectRow(key)
	w.split.SetShowContent(true)
}

// selectRow selects key's sidebar row, which shows its page.
func (w *window) selectRow(key string) {
	for c := w.sidebar.FirstChild(); c != nil; c = gtk.BaseWidget(c).NextSibling() {
		if row, ok := c.(*gtk.ListBoxRow); ok && row.Name() == key {
			w.sidebar.SelectRow(row)
			w.showPage(key) // in case it was already selected
			return
		}
	}
}

// showPage builds and shows the page for key.
func (w *window) showPage(key string) {
	res := w.last
	if res == nil {
		return
	}
	var page *gtk.ScrolledWindow
	title := "Overview"
	switch key {
	case keyOverview:
		page = overviewPage(res, w)
	case keyOther:
		page, title = otherPage(res), "Other Checks"
	default:
		r, ok := res.result(key)
		if !ok {
			return
		}
		page, title = checkPage(r, w), r.Title
	}
	w.sel = key
	w.scroll = page
	w.contentView.SetContent(page)
	w.contentPage.SetTitle(title)
	w.contentTitle.SetTitle(title)
	w.SetTitle("Tidy It – " + title)
}

// withHeader gives a page a plain header bar, for the states before
// there are results.
func withHeader(child gtk.Widgetter) *adw.ToolbarView {
	v := adw.NewToolbarView()
	v.AddTopBar(adw.NewHeaderBar())
	v.SetContent(child)
	return v
}

// showFailure replaces the content with an error page.
func (w *window) showFailure(title, desc string) {
	page := adw.NewStatusPage()
	page.SetIconName("dialog-error-symbolic")
	page.SetTitle(title)
	page.SetDescription(desc)
	retry := gtk.NewButtonWithLabel("Check Again")
	retry.AddCSSClass("pill")
	retry.AddCSSClass("suggested-action")
	retry.SetHAlign(gtk.AlignCenter)
	retry.SetActionName("win.rescan")
	page.SetChild(retry)
	if old := w.stack.ChildByName("failure"); old != nil {
		w.stack.Remove(old)
	}
	w.stack.AddNamed(withHeader(page), "failure")
	w.stack.SetVisibleChildName("failure")
	w.save.SetEnabled(false)
}

func (w *window) toast(msg string) {
	t := adw.NewToast(msg)
	t.SetTimeout(3)
	w.toasts.AddToast(t)
}

// copy puts text on the clipboard.
func (w *window) copy(text, what string) {
	w.Clipboard().SetText(text)
	w.toast(what + " copied")
}

// showInFiles opens the folder holding path, with path selected.
func (w *window) showInFiles(path string) {
	l := gtk.NewFileLauncher(gio.NewFileForPath(path))
	l.OpenContainingFolder(context.Background(), &w.Window, func(res gio.AsyncResulter) {
		if err := l.OpenContainingFolderFinish(res); err != nil && !isDismissed(err) {
			w.toast("Can't open the folder: " + err.Error())
		}
	})
}

func (w *window) saveReport() {
	res := w.last
	if res == nil {
		return
	}
	d := gtk.NewFileDialog()
	d.SetTitle("Save Report")
	d.SetInitialName(fmt.Sprintf("tidyit-%s-%s.html", res.rep.Host, res.rep.StartedAt.Format("2006-01-02")))
	d.Save(context.Background(), &w.Window, func(ar gio.AsyncResulter) {
		f, err := d.SaveFinish(ar)
		if err != nil {
			if !isDismissed(err) {
				w.toast("Not saved: " + err.Error())
			}
			return
		}
		path := f.Path()
		if path == "" {
			w.toast("Not saved: choose a local folder")
			return
		}
		if err := htmlreport.WriteFile(path, res.rep, res.groups, htmlreport.Options{Headline: app.Headline}); err != nil {
			w.toast("Not saved: " + err.Error())
			return
		}
		w.toast("Report saved")
	})
}

// isDismissed reports whether err is the user cancelling a dialog.
func isDismissed(err error) bool {
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "dismissed") || strings.Contains(s, "cancel")
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
