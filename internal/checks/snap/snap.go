// Package snap is a bundled plugin for snapd. It runs when snap is installed.
package snap

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"tidy/internal/engine"
	"tidy/internal/execx"
	"tidy/pkg/manifest"
	"tidy/pkg/report"
)

const snapsDir = "/var/lib/snapd/snaps"

// Plugin returns the snap plugin.
func Plugin() *engine.Plugin {
	return &engine.Plugin{
		Manifest: manifest.Manifest{
			ID: "snap", Name: "Snap", Version: "0.1.0", Protocol: manifest.ProtocolVersion,
			Requires: []string{"snap"},
			Checks: []manifest.CheckSpec{{
				ID:          "old-revisions",
				Title:       "Old snap revisions",
				Description: "snapd keeps previous revisions of each snap so it can roll back.",
				Hint:        "keep fewer in future: sudo snap set system refresh.retain=2",
			}, {
				ID:          "unused-support",
				Title:       "Unused snap runtimes",
				Description: "Bases and content snaps (shared libraries, themes) that no installed snap uses, usually left behind when an app was removed.",
				Hint:        "check first: snap connections <name>",
			}},
		},
		Checks: map[string]engine.CheckFunc{"old-revisions": oldRevisions, "unused-support": unusedSupport},
	}
}

type revision struct{ name, rev string }

// parseDisabled reads `snap list --all` and returns the disabled revisions.
func parseDisabled(out []byte) []revision {
	var revs []revision
	sc := bufio.NewScanner(bytes.NewReader(out))
	header := true
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if header {
			header = false
			continue
		}
		// Name Version Rev Tracking Publisher Notes; only Notes can be "-".
		if len(f) < 4 {
			continue
		}
		for _, note := range strings.Split(f[len(f)-1], ",") {
			if note == "disabled" {
				revs = append(revs, revision{name: f[0], rev: f[2]})
				break
			}
		}
	}
	return revs
}

func oldRevisions(ctx context.Context, _ engine.Input) (engine.Outcome, error) {
	raw, err := execx.Output(ctx, "snap", "list", "--all")
	if err != nil {
		return engine.Outcome{}, err
	}
	var out engine.Outcome
	for _, r := range parseDisabled(raw) {
		path := filepath.Join(snapsDir, r.name+"_"+r.rev+".snap")
		var size int64
		if fi, err := os.Stat(path); err == nil {
			if st, ok := fi.Sys().(*syscall.Stat_t); ok {
				size = st.Blocks * 512
			}
		}
		out.Items = append(out.Items, report.Item{
			ID: r.name + "_" + r.rev, Label: r.name, Detail: "revision " + r.rev,
			Path: path, Size: size, Confidence: report.ConfidenceSafe,
			Action: &report.Action{Command: fmt.Sprintf("sudo snap remove %s --revision=%s", r.name, r.rev), NeedsRoot: true},
		})
	}
	sort.SliceStable(out.Items, func(i, j int) bool { return out.Items[i].Size > out.Items[j].Size })
	return out, nil
}
