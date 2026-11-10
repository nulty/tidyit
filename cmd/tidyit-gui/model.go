package main

import (
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"tidyit/internal/fsx"
	"tidyit/pkg/report"
)

// This file holds the window's view model: plain functions over the report
// that decide what the widgets show. Nothing here touches GTK, so it is
// tested directly.

// Sidebar keys for the pages that aren't a check. A check's key is its
// result id.
const (
	keyOverview = "overview"
	keyOther    = "other"
)

// confGroup is one sidebar section: the flagged checks whose least safe
// item has this confidence.
type confGroup struct {
	Conf    report.Confidence
	Name    string
	Results []report.Result
}

// groupByConfidence splits flagged checks into Safe to remove, Worth
// reviewing and Your call, keeping their order and dropping empty groups.
func groupByConfidence(flagged []report.Result) []confGroup {
	groups := []confGroup{
		{Conf: report.ConfidenceSafe, Name: "Safe to remove"},
		{Conf: report.ConfidenceReview, Name: "Worth reviewing"},
		{Conf: report.ConfidenceManual, Name: "Your call"},
	}
	for _, r := range flagged {
		for i := range groups {
			if groups[i].Conf == r.Caution() {
				groups[i].Results = append(groups[i].Results, r)
			}
		}
	}
	out := groups[:0]
	for _, g := range groups {
		if len(g.Results) > 0 {
			out = append(out, g)
		}
	}
	return out
}

// checkIcons maps a result id, then a plugin id, to a symbolic icon.
var checkIcons = map[string]string{
	"downloads":        "folder-download-symbolic",
	"user-cache":       "folder-symbolic",
	"tmp":              "folder-symbolic",
	"stale-app-config": "preferences-system-symbolic",
	"large-files":      "text-x-generic-symbolic",
	"trash":            "user-trash-symbolic",
	"disk":             "drive-harddisk-symbolic",
	"autostart":        "system-run-symbolic",
	"docker":           "package-x-generic-symbolic",
	"snap":             "application-x-addon-symbolic",
	"flatpak":          "application-x-addon-symbolic",
	"apt":              "system-software-install-symbolic",
	"networkmanager":   "network-wireless-symbolic",
	"systemd":          "emblem-system-symbolic",
	"bleachbit":        "edit-clear-all-symbolic",
}

// checkIcon is the icon shown beside a check.
func checkIcon(r report.Result) string {
	if icon, ok := checkIcons[r.ID]; ok {
		return icon
	}
	if icon, ok := checkIcons[r.Plugin]; ok {
		return icon
	}
	return "application-x-executable-symbolic" // an external plugin
}

// checkFacts is a check's total size and item count, e.g. "4.1G · 17 items".
func checkFacts(r report.Result) string {
	var parts []string
	if n := r.TotalSize(); n > 0 {
		parts = append(parts, fsx.Human(n))
	}
	parts = append(parts, plural(len(r.Items), "item", "items"))
	return strings.Join(parts, " · ")
}

// itemSubtitle is an item's detail and, when neither its label nor its
// detail already shows it, its path.
func itemSubtitle(it report.Item) string {
	var parts []string
	if it.Detail != "" {
		parts = append(parts, it.Detail)
	}
	if it.Path != "" && !strings.Contains(it.Label, it.Path) && !strings.Contains(it.Detail, it.Path) {
		parts = append(parts, it.Path)
	}
	return strings.Join(parts, " · ")
}

// commands lists the commands of every item that has one, in order.
func commands(r report.Result) []string {
	var out []string
	for _, it := range r.Items {
		if it.Action != nil && it.Action.Command != "" {
			out = append(out, it.Action.Command)
		}
	}
	return out
}

// mixed reports whether a check's items differ in confidence, so each
// item needs its own badge.
func mixed(items []report.Item) bool {
	for _, it := range items[min(1, len(items)):] {
		if it.Confidence != items[0].Confidence {
			return true
		}
	}
	return false
}

// ranCount is how many checks ran, whether or not they flagged anything.
func ranCount(results []report.Result) int {
	n := 0
	for _, r := range results {
		if r.Status != report.StatusSkipped {
			n++
		}
	}
	return n
}

// mountUsage is one filesystem from the disk check.
type mountUsage struct {
	Mount   string
	Percent int
	Free    string
	Flagged bool
}

// diskUsage reads the disk check's notes, one line per filesystem as
// "MOUNT  NN% used  FREE free of SIZE", marking the mounts it flagged.
// It returns the first flagged mount, else /, else the first, plus the
// disk check's summary (e.g. "at or above 85% full") when it flagged.
func diskUsage(results []report.Result) (primary *mountUsage, others []mountUsage, summary string) {
	var all []mountUsage
	for _, r := range results {
		if r.ID != "disk.usage" {
			continue
		}
		flagged := map[string]bool{}
		for _, it := range r.Items {
			flagged[it.Label] = true
		}
		for _, line := range r.Notes {
			if m, ok := parseMountNote(line); ok {
				m.Flagged = flagged[m.Mount]
				all = append(all, m)
			}
		}
		if r.Status == report.StatusFlag {
			summary = r.Summary
		}
	}
	if len(all) == 0 {
		return nil, nil, ""
	}
	pick := -1
	for i, m := range all {
		if m.Flagged {
			pick = i
			break
		}
	}
	for i, m := range all {
		if pick < 0 && m.Mount == "/" {
			pick = i
		}
	}
	pick = max(pick, 0)
	p := all[pick]
	for i, m := range all {
		if i != pick && m.Flagged {
			others = append(others, m)
		}
	}
	return &p, others, summary
}

// parseMountNote parses "MOUNT  NN% used  FREE free of SIZE". The mount
// may contain spaces, so it is everything before the "NN%" field.
func parseMountNote(line string) (mountUsage, bool) {
	f := strings.Fields(line)
	for i := 1; i+3 < len(f); i++ {
		if !strings.HasSuffix(f[i], "%") || f[i+1] != "used" || f[i+3] != "free" {
			continue
		}
		pct, err := strconv.Atoi(strings.TrimSuffix(f[i], "%"))
		if err != nil {
			return mountUsage{}, false
		}
		return mountUsage{Mount: strings.Join(f[:i], " "), Percent: pct, Free: f[i+2]}, true
	}
	return mountUsage{}, false
}

// sentence capitalises the first letter of s; checks write hints and
// reasons in lower case so they read well after a colon in the terminal.
func sentence(s string) string {
	r, n := utf8.DecodeRuneInString(s)
	if n == 0 {
		return s
	}
	return string(unicode.ToUpper(r)) + s[n:]
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}
