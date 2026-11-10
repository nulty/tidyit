// Package apt is a bundled plugin for apt/dpkg systems. Its checks share the
// "dpkg" lock so they never read the package database at the same time.
package apt

import (
	"bufio"
	"bytes"
	"context"
	"sort"
	"strconv"
	"strings"

	"tidyit/internal/engine"
	"tidyit/internal/execx"
	"tidyit/pkg/manifest"
	"tidyit/pkg/report"
)

// Plugin returns the apt plugin.
func Plugin() *engine.Plugin {
	return &engine.Plugin{
		Manifest: manifest.Manifest{
			ID: "apt", Name: "APT", Version: "0.1.0", Protocol: manifest.ProtocolVersion,
			Requires: []string{"apt-get", "dpkg-query"},
			Lock:     "dpkg",
			Checks: []manifest.CheckSpec{
				{
					ID:          "autoremove",
					Title:       "Unneeded packages",
					Description: "Installed automatically as dependencies of packages that are gone.",
					Hint:        "sudo apt autoremove --purge",
				},
				{
					ID:          "leftover-config",
					Title:       "Leftover package configs",
					Description: "Packages removed without --purge keep their files in /etc.",
					Hint:        "sudo apt purge $(dpkg-query -W -f '${db:Status-Abbrev} ${binary:Package}\\n' | awk '/^rc/{print $2}')",
				},
			},
		},
		Checks: map[string]engine.CheckFunc{
			"autoremove":      autoremove,
			"leftover-config": leftoverConfig,
		},
	}
}

// parseAutoremove reads `apt-get -s autoremove` and returns the packages
// it would remove ("Remv name [version]").
func parseAutoremove(out []byte) []string {
	var pkgs []string
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) >= 2 && f[0] == "Remv" {
			pkgs = append(pkgs, f[1])
		}
	}
	return pkgs
}

// parseStatus reads `dpkg-query -W -f '${db:Status-Abbrev}\t${binary:Package}\t${Installed-Size}\n'`
// into package -> status abbreviation and package -> size in bytes.
func parseStatus(out []byte) (status map[string]string, size map[string]int64) {
	status, size = map[string]string{}, map[string]int64{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		f := strings.Split(sc.Text(), "\t")
		if len(f) < 3 {
			continue
		}
		name := f[1]
		status[name] = strings.TrimSpace(f[0])
		if kb, err := strconv.ParseInt(strings.TrimSpace(f[2]), 10, 64); err == nil {
			size[name] = kb * 1024
		}
	}
	return status, size
}

func dpkgStatus(ctx context.Context) (map[string]string, map[string]int64, error) {
	raw, err := execx.Output(ctx, "dpkg-query", "-W", "-f", "${db:Status-Abbrev}\t${binary:Package}\t${Installed-Size}\n")
	if err != nil {
		return nil, nil, err
	}
	st, sz := parseStatus(raw)
	return st, sz, nil
}

// sizeOf finds a package's size whether or not apt printed its :arch.
func sizeOf(sizes map[string]int64, pkg string) int64 {
	if n, ok := sizes[pkg]; ok {
		return n
	}
	for _, arch := range []string{"amd64", "arm64", "all", "i386"} {
		if n, ok := sizes[pkg+":"+arch]; ok {
			return n
		}
	}
	return 0
}

func autoremove(ctx context.Context, _ engine.Input) (engine.Outcome, error) {
	raw, err := execx.Output(ctx, "apt-get", "-s", "autoremove")
	if err != nil {
		return engine.Outcome{}, err
	}
	pkgs := parseAutoremove(raw)
	if len(pkgs) == 0 {
		return engine.Outcome{}, nil
	}
	_, sizes, err := dpkgStatus(ctx)
	if err != nil {
		return engine.Outcome{}, err
	}
	var out engine.Outcome
	for _, p := range pkgs {
		size := sizeOf(sizes, p)
		out.Items = append(out.Items, report.Item{
			ID: p, Label: p, Size: size, Confidence: report.ConfidenceSafe,
			Action: &report.Action{Command: "sudo apt-get purge " + p, NeedsRoot: true},
		})
	}
	sort.SliceStable(out.Items, func(i, j int) bool { return out.Items[i].Size > out.Items[j].Size })
	out.Summary = "sizes are installed sizes"
	return out, nil
}

func leftoverConfig(ctx context.Context, _ engine.Input) (engine.Outcome, error) {
	status, _, err := dpkgStatus(ctx)
	if err != nil {
		return engine.Outcome{}, err
	}
	var pkgs []string
	for p, s := range status {
		if s == "rc" {
			pkgs = append(pkgs, p)
		}
	}
	sort.Strings(pkgs)
	var out engine.Outcome
	for _, p := range pkgs {
		out.Items = append(out.Items, report.Item{
			ID: p, Label: p, Confidence: report.ConfidenceSafe,
			Action: &report.Action{Command: "sudo apt-get purge " + p, NeedsRoot: true},
		})
	}
	return out, nil
}
