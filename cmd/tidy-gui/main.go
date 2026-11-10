// Command tidy-gui is the GTK4/libadwaita front end for tidy. Like the CLI
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

	"tidy/internal/app"
	"tidy/internal/config"
	"tidy/internal/engine"
	"tidy/pkg/report"
	"tidy/render/htmlreport"
	"tidy/render/view"
)

const appID = "dev.tidy.Tidy"

func main() {
	a := adw.NewApplication(appID, gio.ApplicationFlagsNone)
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

// window is one tidy window. Every field is touched only on the GTK main
// thread; the run goroutine hands results back with glib.IdleAdd.
type window struct {
	*adw.ApplicationWindow
	toasts   *adw.ToastOverlay
	title    *adw.WindowTitle
	stack    *gtk.Stack
	progress *adw.StatusPage
	results  *adw.PreferencesPage // replaced on every run
	rescan   *gio.SimpleAction
	save     *gio.SimpleAction

	cancel  context.CancelFunc // stops the current run, nil when idle
	closing bool
	last    *finished // the last completed run, for saving
}

// finished is a completed run.
type finished struct {
	rep      *report.Report
	groups   []view.Group
	warnings []string
}

func newWindow(a *adw.Application) *window {
	w := &window{ApplicationWindow: adw.NewApplicationWindow(&a.Application)}
	w.SetDefaultSize(760, 820)
	w.SetTitle("Tidy")

	w.title = adw.NewWindowTitle("Tidy", "")
	header := adw.NewHeaderBar()
	header.SetTitleWidget(w.title)

	refresh := gtk.NewButtonFromIconName("view-refresh-symbolic")
	refresh.SetTooltipText("Check Again (Ctrl+R)")
	refresh.SetActionName("win.rescan")
	header.PackStart(refresh)

	menu := gio.NewMenu()
	menu.Append("Save Report as HTML…", "win.save")
	menuButton := gtk.NewMenuButton()
	menuButton.SetIconName("open-menu-symbolic")
	menuButton.SetTooltipText("Main Menu")
	menuButton.SetMenuModel(menu)
	menuButton.SetPrimary(true)
	header.PackEnd(menuButton)

	w.progress = adw.NewStatusPage()
	w.progress.SetIconName("edit-find-symbolic")
	w.progress.SetTitle("Checking…")
	spinner := gtk.NewSpinner()
	spinner.SetSizeRequest(32, 32)
	spinner.Start()
	w.progress.SetChild(spinner)

	w.stack = gtk.NewStack()
	w.stack.SetTransitionType(gtk.StackTransitionTypeCrossfade)
	w.stack.AddNamed(w.progress, "progress")

	content := adw.NewToolbarView()
	content.AddTopBar(header)
	content.SetContent(w.stack)
	w.toasts = adw.NewToastOverlay()
	w.toasts.SetChild(content)
	w.SetContent(w.toasts)

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
	w.title.SetSubtitle("")
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
		res := &finished{rep: rep, groups: app.Groups(rep, reg), warnings: warnings}
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

func (w *window) showResults(res *finished) {
	w.last = res
	w.save.SetEnabled(true)
	rep := res.rep
	w.title.SetSubtitle(fmt.Sprintf("%s · %s · took %s", rep.Host,
		rep.StartedAt.Format("Mon 2 Jan 15:04"), rep.FinishedAt.Sub(rep.StartedAt).Round(100*time.Millisecond)))

	page := buildPage(res, w)
	if w.results != nil {
		w.stack.Remove(w.results)
	}
	w.results = page
	w.stack.AddNamed(page, "results")
	w.stack.SetVisibleChildName("results")
	if dir := screenshotDir(); dir != "" {
		w.takeScreenshots(dir)
	}
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
	w.stack.AddNamed(page, "failure")
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
	d.SetInitialName(fmt.Sprintf("tidy-%s-%s.html", res.rep.Host, res.rep.StartedAt.Format("2006-01-02")))
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
