// Package systemd is a bundled plugin for failed units and the journal.
package systemd

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"tidy/internal/engine"
	"tidy/internal/execx"
	"tidy/internal/fsx"
	"tidy/pkg/manifest"
	"tidy/pkg/report"
)

// Plugin returns the systemd plugin.
func Plugin() *engine.Plugin {
	return &engine.Plugin{
		Manifest: manifest.Manifest{
			ID: "systemd", Name: "Services and logs", Version: "0.1.0", Protocol: manifest.ProtocolVersion,
			Requires: []string{"systemctl"},
			Checks: []manifest.CheckSpec{
				{
					ID:    "failed-units",
					Title: "Failed systemd units",
					Hint:  "details: systemctl [--user] status <unit>   clear once fixed: systemctl [--user] reset-failed <unit>",
				},
				{
					ID:    "journal-size",
					Title: "Systemd journal size",
					Hint:  "trim: sudo journalctl --vacuum-time=4weeks   cap it: SystemMaxUse= in /etc/systemd/journald.conf",
					Options: map[string]manifest.Option{
						"warn_mb": {Type: manifest.TypeInteger, Default: 1024, Min: manifest.Ptr(1),
							Description: "Flag the journal when it uses at least this much disk"},
					},
				},
			},
		},
		Checks: map[string]engine.CheckFunc{
			"failed-units": failedUnits,
			"journal-size": journalSize,
		},
	}
}

type unit struct{ name, description string }

// parseFailed reads `systemctl --failed --no-legend --plain`:
// UNIT LOAD ACTIVE SUB DESCRIPTION...
func parseFailed(out []byte) []unit {
	var units []unit
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 0 {
			continue
		}
		u := unit{name: f[0]}
		if len(f) > 4 {
			u.description = strings.Join(f[4:], " ")
		}
		units = append(units, u)
	}
	return units
}

func failedUnits(ctx context.Context, _ engine.Input) (engine.Outcome, error) {
	var out engine.Outcome
	nSys, nUser := 0, 0
	for _, scope := range []string{"system", "user"} {
		args := []string{"--failed", "--no-legend", "--plain", "--no-pager"}
		if scope == "user" {
			args = append([]string{"--user"}, args...)
		}
		raw, err := execx.Output(ctx, "systemctl", args...)
		if err != nil {
			if scope == "user" { // no user manager (e.g. over ssh without lingering)
				out.Notes = append(out.Notes, "user units not checked: "+err.Error())
				continue
			}
			return engine.Outcome{}, err
		}
		flag := ""
		if scope == "user" {
			flag = "--user "
		}
		for _, u := range parseFailed(raw) {
			detail := u.description
			if scope == "user" {
				detail = strings.TrimSpace(detail + " (user unit)")
				nUser++
			} else {
				nSys++
			}
			out.Items = append(out.Items, report.Item{
				ID: scope + ":" + u.name, Label: u.name, Detail: detail, Confidence: report.ConfidenceManual,
				Action: &report.Action{Command: "systemctl " + flag + "status " + u.name},
			})
		}
	}
	if nUser > 0 {
		out.Summary = fmt.Sprintf("%d system, %d user", nSys, nUser)
	}
	return out, nil
}

var usageRe = regexp.MustCompile(`take up ([0-9.]+)([BKMGTPE]?)`)

// parseDiskUsage reads `journalctl --disk-usage`, which prints sizes in
// powers of 1024 ("... take up 740.3M in the file system.").
func parseDiskUsage(out []byte) (int64, bool) {
	m := usageRe.FindSubmatch(out)
	if m == nil {
		return 0, false
	}
	f, err := strconv.ParseFloat(string(m[1]), 64)
	if err != nil {
		return 0, false
	}
	exp := strings.Index("BKMGTPE", string(m[2]))
	if exp < 0 {
		exp = 0
	}
	for ; exp > 0; exp-- {
		f *= 1024
	}
	return int64(f), true
}

func journalSize(ctx context.Context, in engine.Input) (engine.Outcome, error) {
	raw, err := execx.Output(ctx, "journalctl", "--disk-usage")
	if err != nil {
		return engine.Outcome{}, err
	}
	size, ok := parseDiskUsage(raw)
	if !ok {
		return engine.Outcome{}, fmt.Errorf("could not read journalctl --disk-usage output: %q", strings.TrimSpace(string(raw)))
	}
	out := engine.Outcome{Summary: fsx.Human(size)}
	if size >= int64(in.Options.Int("warn_mb"))<<20 {
		out.Summary = ""
		out.Items = []report.Item{{
			ID: "journal", Label: "systemd journal", Path: "/var/log/journal", Size: size,
			Detail:     "vacuuming keeps recent logs, so it frees part of this",
			Confidence: report.ConfidenceSafe,
			Action:     &report.Action{Command: "sudo journalctl --vacuum-time=4weeks", NeedsRoot: true},
		}}
	}
	return out, nil
}
