package snap

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"tidyit/internal/engine"
	"tidyit/internal/fsx"
	"tidyit/pkg/report"
)

// neverOpened is an app snap the current user has never run, with the
// support snaps that only it needs.
type neverOpened struct {
	snap  snapInfo
	frees []snapInfo
}

// findNeverOpened returns app snaps that have commands a person runs (not
// only system services), that no other snap uses, and that have never
// created a per-user folder (~/snap/<name>, made the first time one of the
// snap's apps runs for a user) in any account. used reports whether any
// account has that folder.
func findNeverOpened(snaps []snapInfo, conns connections, used func(string) bool) []neverOpened {
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
		if systemService || used(n) {
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
	readable, private := accountSnapDirs(passwdFile)
	usedByAnyone := func(name string) bool {
		for _, dir := range readable {
			if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
				return true
			}
		}
		return false
	}
	found := findNeverOpened(snaps, conns, usedByAnyone)
	if len(found) > 0 && len(private) > 0 {
		// Another account may use these apps and tidyit can't see; saying
		// nothing beats suggesting the removal of someone else's app.
		return engine.Outcome{}, engine.Skip("can't see whether other accounts use these apps (%s: home folder private); to check every account: sudo tidyit --check snap.never-opened",
			strings.Join(private, ", "))
	}
	seed := seeded()
	var out engine.Outcome
	for _, no := range found {
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
	if len(out.Items) > 0 && len(readable) > 1 {
		out.Summary = fmt.Sprintf("checked all %d accounts", len(readable))
	}
	return out, nil
}

const passwdFile = "/etc/passwd"

// accountSnapDirs returns ~/snap for every account a person logs in to
// (UIDs 1000 and up with a login shell; root does not run desktop apps), split into those tidyit
// can read and the names of accounts whose home it cannot read. A home
// with no snap folder is readable: that account has never run a snap.
func accountSnapDirs(passwd string) (readable []string, private []string) {
	data, err := os.ReadFile(passwd)
	if err != nil {
		return []string{filepath.Join(fsx.Home(), "snap")}, nil
	}
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Split(line, ":")
		if len(f) < 7 {
			continue
		}
		uid, err := strconv.Atoi(f[2])
		if err != nil || uid < 1000 || uid >= 65534 {
			continue
		}
		if shell := f[6]; strings.HasSuffix(shell, "nologin") || strings.HasSuffix(shell, "false") {
			continue
		}
		home := f[5]
		if _, err := os.Stat(home); err != nil {
			continue // account without a home folder
		}
		dir := filepath.Join(home, "snap")
		if _, err := os.ReadDir(dir); err != nil && !errors.Is(err, fs.ErrNotExist) {
			private = append(private, f[0])
			continue
		}
		readable = append(readable, dir)
	}
	return readable, private
}
