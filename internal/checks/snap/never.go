package snap

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"tidy/internal/engine"
	"tidy/internal/fsx"
	"tidy/pkg/report"
)

// neverOpened is an app snap the current user has never run, with the
// support snaps that only it needs.
type neverOpened struct {
	snap  snapInfo
	frees []snapInfo
}

// findNeverOpened returns app snaps that have commands a person runs (not
// only system services), that no other snap uses, and that have never
// created their per-user folder (~/snap/<name>), which snapd makes the
// first time one of the snap's apps runs for that user. hasUserDir
// reports whether that folder exists.
func findNeverOpened(snaps []snapInfo, conns connections, hasUserDir func(string) bool) []neverOpened {
	g := newGraph(snaps, conns)
	already := map[string]bool{}
	for _, u := range g.unusedWith(nil) {
		already[u.snap.Name] = true
	}
	var out []neverOpened
	for _, n := range g.names {
		s := g.installed[n]
		if s.Type != "app" || len(s.Apps) == 0 || g.supportKind(s) != "" || len(g.users[n]) > 0 {
			continue
		}
		systemService := false
		for _, a := range s.Apps {
			if a.Daemon != "" && a.DaemonScope != "user" {
				systemService = true
			}
		}
		if systemService || hasUserDir(n) {
			continue
		}
		no := neverOpened{snap: s}
		for _, u := range g.unusedWith(map[string]bool{n: true}) {
			if !already[u.snap.Name] {
				no.frees = append(no.frees, u.snap)
			}
		}
		out = append(out, no)
	}
	return out
}

func neverOpenedApps(ctx context.Context, _ engine.Input) (engine.Outcome, error) {
	var snaps []snapInfo
	if err := snapd(ctx, "/v2/snaps", &snaps); err != nil {
		return engine.Outcome{}, err
	}
	var conns connections
	if err := snapd(ctx, "/v2/connections?select=all", &conns); err != nil {
		return engine.Outcome{}, err
	}
	home := fsx.Home()
	hasUserDir := func(name string) bool {
		_, err := os.Stat(filepath.Join(home, "snap", name))
		return err == nil
	}
	seed := seeded()
	var out engine.Outcome
	for _, no := range findNeverOpened(snaps, conns, hasUserDir) {
		s := no.snap
		path, size := s.file()
		names := []string{s.Name}
		var freed []string
		for _, f := range no.frees {
			_, fs := f.file()
			size += fs
			names = append(names, f.Name)
			freed = append(freed, f.Name+" "+fsx.Human(fs))
		}
		detail := "never opened"
		conf := report.ConfidenceReview
		if seed[s.Name] {
			detail += ", came with the system"
			conf = report.ConfidenceManual
		} else if !s.InstallDate.IsZero() {
			detail += ", current revision from " + s.InstallDate.Format("2006-01-02")
		}
		if len(freed) > 0 {
			detail += "; size includes " + strings.Join(freed, ", ") + ", which only it needs"
		}
		out.Items = append(out.Items, report.Item{
			ID: s.Name, Label: s.Name, Detail: detail, Path: path, Size: size, Confidence: conf,
			Action: &report.Action{Command: "sudo snap remove " + strings.Join(names, " "), NeedsRoot: true},
		})
	}
	sort.SliceStable(out.Items, func(i, j int) bool { return out.Items[i].Size > out.Items[j].Size })
	if len(out.Items) > 0 {
		out.Summary = "for your account only: another user may run them"
	}
	return out, nil
}
