// Command tidyit reports stale things on a Linux machine. It is read-only.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"tidyit/internal/app"
	"tidyit/internal/config"
	"tidyit/internal/engine"
	"tidyit/internal/version"
	"tidyit/pkg/report"
	"tidyit/render/htmlreport"
	"tidyit/render/term"
	"tidyit/render/view"
)

const usage = `tidyit: report stale things on this machine (read-only)

Usage:
  tidyit [run] [flags]     run checks and print a report
  tidyit checks [-v]       list checks, their options and where settings come from
  tidyit version           print the version

Run flags:
`

func main() {
	args := os.Args[1:]
	cmd := "run"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	var err error
	if len(args) > 0 && (args[0] == "--version" || args[0] == "-version") {
		cmd = "version"
	}
	switch cmd {
	case "version":
		fmt.Println(version.Generator())
	case "run":
		err = runCmd(args)
	case "checks":
		err = checksCmd(args)
	case "help":
		fmt.Print(usage)
		fs, _ := runFlags(os.Stdout)
		fs.PrintDefaults()
	default:
		err = fmt.Errorf("unknown command %q (try: tidyit help)", cmd)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "tidyit:", err)
		os.Exit(2)
	}
}

type runOpts struct {
	json, onlyFlagged, verbose, full bool
	checks, configPath, html         string
}

func runFlags(out io.Writer) (*flag.FlagSet, *runOpts) {
	o := &runOpts{}
	fs := flag.NewFlagSet("tidyit run", flag.ContinueOnError)
	fs.SetOutput(out)
	fs.BoolVar(&o.json, "json", false, "print the report as JSON")
	fs.StringVar(&o.html, "html", "", "write a self-contained HTML report to this file (- for stdout)")
	fs.BoolVar(&o.onlyFlagged, "only-flagged", false, "hide checks that found nothing or were skipped")
	fs.BoolVar(&o.full, "full", false, "list every flagged item with its command, grouped by plugin")
	fs.BoolVar(&o.verbose, "v", false, "like --full, plus every item (no cap), timings, notes and plugin debug output")
	fs.StringVar(&o.checks, "check", "", "comma-separated check ids, globs or plugin ids to run, e.g. docker,downloads (runs them even if off)")
	fs.StringVar(&o.configPath, "config", "", "config file (default "+config.DefaultPath()+")")
	return fs, o
}

func runCmd(args []string) error {
	fs, o := runFlags(os.Stderr)
	fs.Usage = func() { fmt.Fprint(os.Stderr, usage); fs.PrintDefaults() }
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := app.LoadConfig(o.configPath)
	if err != nil {
		return err
	}
	reg, warnings := app.Registry()
	insts, iw := reg.Instances(cfg)
	warnings = append(warnings, iw...)
	var patterns []string
	if o.checks != "" {
		patterns = strings.Split(o.checks, ",")
	}
	if insts, err = engine.Filter(insts, patterns); err != nil {
		return err
	}
	if len(insts) == 0 {
		return fmt.Errorf("no checks match %q (see: tidyit checks)", o.checks)
	}
	for _, w := range warnings {
		fmt.Fprintln(os.Stderr, "tidyit: warning:", w)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	runner := engine.Runner{Concurrency: cfg.Run.Concurrency, Timeout: cfg.Run.Timeout}
	host, _ := os.Hostname()

	progress := newProgress(os.Stderr, app.Enabled(insts), !o.json && isTerminal(os.Stderr))
	started := time.Now()
	rep := engine.Collect(runner.Run(ctx, insts), host, progress.event)
	rep.Generator = version.Generator()
	rep.StartedAt = started
	progress.done()
	app.SortResults(rep, insts)

	if o.html != "" {
		return writeHTML(o.html, rep, app.Groups(rep, reg))
	}
	if o.json {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(rep)
	}
	tty := isTerminal(os.Stdout)
	width := 0
	if tty {
		width = termWidth(os.Stdout)
	}
	term.Render(os.Stdout, rep, app.Groups(rep, reg), term.Options{
		Color:       tty && os.Getenv("NO_COLOR") == "",
		OnlyFlagged: o.onlyFlagged,
		Full:        o.full,
		Verbose:     o.verbose,
		Width:       width,
		Headline:    app.Headline,
	})
	return nil
}

// writeHTML writes the HTML report to path, or stdout for "-".
func writeHTML(path string, rep *report.Report, gs []view.Group) error {
	opt := htmlreport.Options{Headline: app.Headline}
	if path == "-" {
		return htmlreport.Render(os.Stdout, rep, gs, opt)
	}
	if err := htmlreport.WriteFile(path, rep, gs, opt); err != nil {
		return err
	}
	abs, _ := filepath.Abs(path)
	fmt.Fprintf(os.Stderr, "tidyit: wrote %s\n", abs)
	return nil
}

func checksCmd(args []string) error {
	fs := flag.NewFlagSet("tidyit checks", flag.ContinueOnError)
	verbose := fs.Bool("v", false, "show every option with its value and description")
	configPath := fs.String("config", "", "config file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := app.LoadConfig(*configPath)
	if err != nil {
		return err
	}
	reg, warnings := app.Registry()
	insts, iw := reg.Instances(cfg)
	warnings = append(warnings, iw...)
	if cfg.Path != "" {
		fmt.Printf("config: %s\n\n", cfg.Path)
	} else {
		fmt.Printf("config: none (create %s to change settings)\n\n", config.DefaultPath())
	}
	for _, in := range insts {
		state := ""
		switch {
		case !in.Enabled && in.Spec.DefaultOff:
			state = fmt.Sprintf("  (off by default; run with --check %s, or set enabled = true in [checks.%s])", in.ID, in.ID)
		case !in.Enabled:
			state = "  (disabled in config)"
		}
		if in.OptErr != nil {
			state += "  (config error: " + in.OptErr.Error() + ")"
		}
		fmt.Printf("%-34s %s  [%s via %s.%s]%s\n", in.ID, in.Title, in.Origin, in.Plugin.Manifest.ID, in.Spec.ID, state)
		if *verbose && in.Plugin.Dir != "" {
			fmt.Printf("    %-18s   %s\n", "(external plugin)", in.Plugin.Dir)
		}
		if !*verbose || in.OptErr != nil {
			continue
		}
		names := make([]string, 0, len(in.Spec.Options))
		for n := range in.Spec.Options {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			val, _ := json.Marshal(in.Options[n])
			fmt.Printf("    %-18s = %-30s %s\n", n, val, in.Spec.Options[n].Description)
		}
	}
	for _, w := range warnings {
		fmt.Fprintln(os.Stderr, "\ntidyit: warning:", w)
	}
	return nil
}

// termWidth asks the terminal for its width, falling back to $COLUMNS
// and then 100.
func termWidth(f *os.File) int {
	var ws struct{ Row, Col, X, Y uint16 }
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), uintptr(syscall.TIOCGWINSZ), uintptr(unsafe.Pointer(&ws))); errno == 0 && ws.Col > 0 {
		return int(ws.Col)
	}
	if n, err := strconv.Atoi(os.Getenv("COLUMNS")); err == nil && n > 0 {
		return n
	}
	return 100
}

func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// progress shows a one-line live status on a terminal while checks run.
type progress struct {
	w               io.Writer
	total, finished int
	running         map[string]bool
	on              bool
}

func newProgress(w io.Writer, total int, on bool) *progress {
	return &progress{w: w, total: total, running: map[string]bool{}, on: on}
}

func (p *progress) event(ev report.Event) {
	if !p.on {
		return
	}
	switch ev.Type {
	case report.EventStarted:
		p.running[ev.ID] = true
	case report.EventFinished:
		delete(p.running, ev.ID)
		p.finished++
	}
	names := make([]string, 0, len(p.running))
	for id := range p.running {
		names = append(names, id)
	}
	sort.Strings(names)
	line := fmt.Sprintf("  checking %d/%d  %s", p.finished, p.total, strings.Join(names, ", "))
	if len(line) > 100 {
		line = line[:97] + "..."
	}
	fmt.Fprintf(p.w, "\r\033[K%s", line)
}

func (p *progress) done() {
	if p.on {
		fmt.Fprint(p.w, "\r\033[K")
	}
}
