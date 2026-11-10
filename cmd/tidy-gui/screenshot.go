package main

import (
	"fmt"
	"math"
	"os"
	"path/filepath"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

// Screenshot mode is a development aid for design work. With
// TIDY_GUI_SCREENSHOTS=DIR the window, once its results are in, scrolls
// through the page one screenful at a time: first with every check
// collapsed, then with the first few flagged checks and the "found
// nothing" list expanded. Then it quits. At each stop it prints
// "shot DIR/NAME-NN.png" on stdout and holds still for holdMS, for
// scripts/gui-screenshots to capture the window. (GTK's own widget
// snapshot comes back empty for a mapped toplevel, so capture is left to
// the display server.) Set ADW_DEBUG_COLOR_SCHEME=prefer-dark for the
// dark style.
const (
	screenshotEnv = "TIDY_GUI_SCREENSHOTS"
	expandInShots = 3    // flagged checks expanded in the second pass
	maxShots      = 30   // per pass
	settleMS      = 500  // wait for layout and animations before a stop
	holdMS        = 1500 // time the capturing script gets per stop
)

func screenshotDir() string { return os.Getenv(screenshotEnv) }

// takeScreenshots captures the results page in two passes and quits.
func (w *window) takeScreenshots(dir string) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "tidy-gui:", err)
		w.Application().Quit()
		return
	}
	sw := findScrolled(w.results)
	if sw == nil {
		fmt.Fprintln(os.Stderr, "tidy-gui: no scrolled window in the results page")
		w.Application().Quit()
		return
	}
	w.scrollPass(dir, "collapsed", sw, func() {
		exps := findExpanders(w.results)
		for i, e := range exps {
			if i < expandInShots || i == len(exps)-1 {
				e.SetExpanded(true)
			}
		}
		w.scrollPass(dir, "expanded", sw, func() { w.Application().Quit() })
	})
}

// scrollPass scrolls sw from the top one screenful at a time, saving
// DIR/NAME-NN.png at each stop, then calls done.
func (w *window) scrollPass(dir, name string, sw *gtk.ScrolledWindow, done func()) {
	adj := sw.VAdjustment()
	i := 0
	var step func()
	step = func() {
		glib.TimeoutAdd(settleMS, func() bool {
			fmt.Printf("shot %s\n", filepath.Join(dir, fmt.Sprintf("%s-%02d.png", name, i)))
			os.Stdout.Sync()
			i++
			// Overlap screenfuls slightly so nothing falls between shots.
			next := adj.Value() + adj.PageSize()*0.9
			if adj.Value()+adj.PageSize() >= adj.Upper()-1 || i >= maxShots {
				glib.TimeoutAdd(holdMS, func() bool { done(); return false })
				return false
			}
			glib.TimeoutAdd(holdMS, func() bool {
				adj.SetValue(math.Min(next, adj.Upper()-adj.PageSize()))
				step()
				return false
			})
			return false
		})
	}
	adj.SetValue(0)
	step()
}

func findScrolled(root gtk.Widgetter) *gtk.ScrolledWindow {
	var found *gtk.ScrolledWindow
	walk(root, func(c gtk.Widgetter) bool {
		if s, ok := c.(*gtk.ScrolledWindow); ok {
			found = s
			return false
		}
		return true
	})
	return found
}

func findExpanders(root gtk.Widgetter) []*adw.ExpanderRow {
	var out []*adw.ExpanderRow
	walk(root, func(c gtk.Widgetter) bool {
		if e, ok := c.(*adw.ExpanderRow); ok {
			out = append(out, e)
			return false // don't look inside an expander
		}
		return true
	})
	return out
}

// walk visits root's descendants depth first; visit returns false to skip
// a widget's children.
func walk(root gtk.Widgetter, visit func(gtk.Widgetter) bool) {
	for c := gtk.BaseWidget(root).FirstChild(); c != nil; c = gtk.BaseWidget(c).NextSibling() {
		if visit(c) {
			walk(c, visit)
		}
	}
}
