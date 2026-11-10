// Command tidy reports stale things on a Linux machine. It is read-only.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"tidy/internal/checks/apt"
	"tidy/internal/checks/bleachbit"
	"tidy/internal/checks/core"
	"tidy/internal/checks/docker"
	"tidy/internal/checks/flatpak"
	"tidy/internal/checks/network"
	"tidy/internal/checks/snap"
	"tidy/internal/checks/systemd"
	"tidy/internal/config"
	"tidy/internal/engine"
	"tidy/pkg/report"
	"tidy/render/term"
)

const usage = `tidy: report stale things on this machine (read-only)

Usage:
  tidy [run] [flags]     run checks and print a report
  tidy checks [-v]       list checks, their options and where settings come from

Run flags:
`

func main() {
	args := os.Args[1:]
	cmd := "run"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	var err error
	switch cmd {
	case "run":
		err = runCmd(args)
	case "checks":
		err = checksCmd(args)
	case "help":
		fmt.Print(usage)
		fs, _ := runFlags(os.Stdout)
		fs.PrintDefaults()
	default:
		err = fmt.Errorf("unknown command %q (try: tidy help)", cmd)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "tidy:", err)
		os.Exit(2)
	}
}

func registry() *engine.Registry {
	r := &engine.Registry{}
	for _, p := range core.Plugins() {
		r.Add(p)
	}
	r.Add(docker.Plugin())
	r.Add(snap.Plugin())
	r.Add(flatpak.Plugin())
	r.Add(apt.Plugin())
	r.Add(network.Plugin())
	r.Add(systemd.Plugin())
	r.Add(bleachbit.Plugin())
	return r
}

type runOpts struct {
	json, onlyFlagged, verbose bool
	checks                     string
	configPath                 string
}

func runFlags(out io.Writer) (*flag.FlagSet, *runOpts) {
	o := &runOpts{}
	fs := flag.NewFlagSet("tidy run", flag.ContinueOnError)
	fs.SetOutput(out)
	fs.BoolVar(&o.json, "json", false, "print the report as JSON")
	fs.BoolVar(&o.onlyFlagged, "only-flagged", false, "hide checks that found nothing or were skipped")
	fs.BoolVar(&o.verbose, "v", false, "show timings and all notes")
	fs.StringVar(&o.checks, "check", "", "comma-separated check ids, globs or plugin ids to run, e.g. docker,downloads (runs them even if off)")
	fs.StringVar(&o.configPath, "config", "", "config file (default "+config.DefaultPath()+")")
	return fs, o
}

func loadConfig(path string) (*config.Config, error) {
	if path != "" {
		return config.Load(path, true)
	}
	return config.Load(config.DefaultPath(), false)
}

func runCmd(args []string) error {
	fs, o := runFlags(os.Stderr)
	fs.Usage = func() { fmt.Fprint(os.Stderr, usage); fs.PrintDefaults() }
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := loadConfig(o.configPath)
	if err != nil {
		return err
	}
	reg := registry()
	insts, warnings := reg.Instances(cfg)
	var patterns []string
	if o.checks != "" {
		patterns = strings.Split(o.checks, ",")
	}
	if insts, err = engine.Filter(insts, patterns); err != nil {
		return err
	}
	if len(insts) == 0 {
		return fmt.Errorf("no checks match %q (see: tidy checks)", o.checks)
	}
	for _, w := range warnings {
		fmt.Fprintln(os.Stderr, "tidy: warning:", w)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	runner := engine.Runner{Concurrency: cfg.Run.Concurrency, Timeout: cfg.Run.Timeout}
	host, _ := os.Hostname()

	enabled := 0
	for _, in := range insts {
		if in.Enabled {
			enabled++
		}
	}
	progress := newProgress(os.Stderr, enabled, !o.json && isTerminal(os.Stderr))
	started := time.Now()
	rep := engine.Collect(runner.Run(ctx, insts), host, progress.event)
	rep.StartedAt = started
	progress.done()
	sortResults(rep, insts)

	if o.json {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(rep)
	}
	term.Render(os.Stdout, rep, groups(rep, reg), term.Options{
		Color:       isTerminal(os.Stdout) && os.Getenv("NO_COLOR") == "",
		OnlyFlagged: o.onlyFlagged,
		Verbose:     o.verbose,
	})
	return nil
}

// sortResults puts results back in instance order (they arrive in
// completion order).
func sortResults(rep *report.Report, insts []*engine.Instance) {
	pos := map[string]int{}
	for i, in := range insts {
		pos[in.ID] = i
	}
	sort.SliceStable(rep.Results, func(i, j int) bool { return pos[rep.Results[i].ID] < pos[rep.Results[j].ID] })
}

// groups sections the results by plugin, in registry order.
func groups(rep *report.Report, reg *engine.Registry) []term.Group {
	var out []term.Group
	for _, p := range reg.Plugins() {
		g := term.Group{Name: p.Manifest.Name}
		for _, r := range rep.Results {
			if r.Plugin == p.Manifest.ID {
				g.Results = append(g.Results, r)
			}
		}
		if len(g.Results) > 0 {
			out = append(out, g)
		}
	}
	return out
}

func checksCmd(args []string) error {
	fs := flag.NewFlagSet("tidy checks", flag.ContinueOnError)
	verbose := fs.Bool("v", false, "show every option with its value and description")
	configPath := fs.String("config", "", "config file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}
	insts, warnings := registry().Instances(cfg)
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
		fmt.Fprintln(os.Stderr, "\ntidy: warning:", w)
	}
	return nil
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
