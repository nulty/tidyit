package main

import (
	"fmt"
	"math"
	"os"
	"path/filepath"

	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

// Screenshot mode is a development aid for design work. With
// TIDYIT_GUI_SCREENSHOTS=DIR the window, once its results are in, shows the
// overview, the first few flagged checks and Other checks in turn,
// scrolling each one screenful at a time. Then it narrows the window
// below the breakpoint and shows the sidebar and one check. Then it
// quits. At each stop it prints "shot DIR/NAME.png" on stdout and holds
// still for holdMS, for scripts/gui-screenshots to capture the window.
// (GTK's own widget snapshot comes back empty for a mapped toplevel, so
// capture is left to the display server.) Set
// ADW_DEBUG_COLOR_SCHEME=prefer-dark for the dark style.
const (
	screenshotEnv = "TIDYIT_GUI_SCREENSHOTS"
	checksInShots = 3    // flagged checks shown
	maxShots      = 12   // per page
	settleMS      = 600  // wait for layout and animations before a stop
	holdMS        = 1500 // time the capturing script gets per stop
)

func screenshotDir() string { return os.Getenv(screenshotEnv) }

// takeScreenshots captures the pages, then the narrow layout, and quits.
func (w *window) takeScreenshots(dir string) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "tidyit-gui:", err)
		w.Application().Quit()
		return
	}
	keys := []string{keyOverview}
	for i, r := range w.last.overview.Flagged {
		if i < checksInShots {
			keys = append(keys, r.ID)
		}
	}
	keys = append(keys, keyOther)

	var next func(i int)
	next = func(i int) {
		if i == len(keys) {
			w.narrowShots(dir, keys)
			return
		}
		w.open(keys[i])
		w.scrollPass(dir, fmt.Sprintf("wide-%d-%s", i, keys[i]), w.scroll, func() { next(i + 1) })
	}
	next(0)
}

// narrowShots shrinks the window below the breakpoint and captures the
// sidebar, then the first flagged check.
func (w *window) narrowShots(dir string, keys []string) {
	w.SetDefaultSize(400, 780)
	w.split.SetShowContent(false)
	w.stop(dir, "narrow-0-sidebar", func() {
		w.open(keys[min(1, len(keys)-1)])
		w.stop(dir, "narrow-1-check", func() { w.Application().Quit() })
	})
}

// stop waits for the window to settle, announces a shot, holds still for
// the capture, then calls then.
func (w *window) stop(dir, name string, then func()) {
	glib.TimeoutAdd(settleMS, func() bool {
		fmt.Printf("shot %s\n", filepath.Join(dir, name+".png"))
		os.Stdout.Sync()
		glib.TimeoutAdd(holdMS, func() bool { then(); return false })
		return false
	})
}

// scrollPass scrolls sw from the top one screenful at a time, saving
// DIR/NAME-NN.png at each stop, then calls done.
func (w *window) scrollPass(dir, name string, sw *gtk.ScrolledWindow, done func()) {
	adj := sw.VAdjustment()
	adj.SetValue(0)
	i := 0
	var step func()
	step = func() {
		w.stop(dir, fmt.Sprintf("%s-%02d", name, i), func() {
			i++
			if adj.Value()+adj.PageSize() >= adj.Upper()-1 || i >= maxShots {
				done()
				return
			}
			// Overlap screenfuls slightly so nothing falls between shots.
			adj.SetValue(math.Min(adj.Value()+adj.PageSize()*0.9, adj.Upper()-adj.PageSize()))
			step()
		})
	}
	step()
}
