package engine

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"tidy/pkg/report"
)

// Runner runs instances in parallel and streams events.
type Runner struct {
	Concurrency int           // max checks running at once; 0 = 8
	Timeout     time.Duration // per check; 0 = 30s
}

// Run starts every enabled instance and returns a channel of events. The
// channel is closed when all instances have finished. Disabled instances
// are not run and produce no events.
func (r Runner) Run(ctx context.Context, insts []*Instance) <-chan report.Event {
	conc := r.Concurrency
	if conc <= 0 {
		conc = 8
	}
	events := make(chan report.Event, 2*len(insts))
	sem := make(chan struct{}, conc)

	probes := map[*Plugin]func() error{}
	locks := map[string]*sync.Mutex{}
	for _, in := range insts {
		p := in.Plugin
		if _, ok := probes[p]; !ok {
			probes[p] = sync.OnceValue(func() error { return probe(ctx, p) })
		}
		if l := p.Manifest.Lock; l != "" && locks[l] == nil {
			locks[l] = &sync.Mutex{}
		}
	}

	var wg sync.WaitGroup
	for _, in := range insts {
		if !in.Enabled {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if l := locks[in.Plugin.Manifest.Lock]; l != nil {
				l.Lock()
				defer l.Unlock()
			}
			events <- report.Event{Type: report.EventStarted, ID: in.ID, Time: time.Now()}
			res := r.runOne(ctx, in, probes[in.Plugin])
			events <- report.Event{Type: report.EventFinished, ID: in.ID, Time: time.Now(), Result: res}
		}()
	}
	go func() {
		wg.Wait()
		close(events)
	}()
	return events
}

func (r Runner) runOne(ctx context.Context, in *Instance, probe func() error) (res *report.Result) {
	start := time.Now()
	res = &report.Result{
		ID:     in.ID,
		Plugin: in.Plugin.Manifest.ID,
		Check:  in.Spec.ID,
		Title:  in.Title,
	}
	defer func() {
		if p := recover(); p != nil {
			res.Status = report.StatusError
			res.Error = fmt.Sprintf("check crashed: %v", p)
			res.Notes = append(res.Notes, strings.TrimSpace(string(debug.Stack())))
		}
		res.Duration = time.Since(start)
	}()

	if in.OptErr != nil {
		res.Status, res.Error = report.StatusError, "config: "+in.OptErr.Error()
		return res
	}
	if err := probe(); err != nil {
		setErr(res, err)
		return res
	}
	fn := in.Plugin.Checks[in.Spec.ID]
	if fn == nil {
		res.Status, res.Error = report.StatusError, "plugin has no implementation for this check"
		return res
	}

	timeout := in.Timeout
	if timeout == 0 && in.Spec.Timeout != "" {
		timeout, _ = time.ParseDuration(in.Spec.Timeout)
	}
	if timeout == 0 {
		timeout = r.Timeout
	}
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	out, err := fn(cctx, Input{ID: in.ID, Options: in.Options})
	if err != nil {
		if errors.Is(cctx.Err(), context.DeadlineExceeded) {
			err = fmt.Errorf("timed out after %s (raise it with timeout = \"...\" in [checks.%s])", timeout, in.ID)
		}
		setErr(res, err)
		return res
	}
	res.Items, res.Summary, res.Notes = out.Items, out.Summary, out.Notes
	if len(out.Items) > 0 {
		res.Status = report.StatusFlag
		res.Hint = in.Hint
		if out.Hint != "" {
			res.Hint = out.Hint
		}
	} else {
		res.Status = report.StatusOK
	}
	return res
}

func setErr(res *report.Result, err error) {
	if s, ok := asSkip(err); ok {
		res.Status, res.SkipReason = report.StatusSkipped, s.Reason
		return
	}
	res.Status, res.Error = report.StatusError, err.Error()
}

// probe checks a plugin's required binaries, then runs its own probe.
func probe(ctx context.Context, p *Plugin) error {
	for _, bin := range p.Manifest.Requires {
		if _, err := exec.LookPath(bin); err != nil {
			return Skip("%s not installed", bin)
		}
	}
	if p.Probe != nil {
		return p.Probe(ctx)
	}
	return nil
}

// Collect drains an event stream into a Report.
func Collect(events <-chan report.Event, host string, onEvent func(report.Event)) *report.Report {
	rep := &report.Report{SchemaVersion: report.SchemaVersion, Host: host, StartedAt: time.Now()}
	for ev := range events {
		if onEvent != nil {
			onEvent(ev)
		}
		if ev.Type == report.EventFinished && ev.Result != nil {
			rep.Results = append(rep.Results, *ev.Result)
		}
	}
	rep.FinishedAt = time.Now()
	return rep
}
