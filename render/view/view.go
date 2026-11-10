// Package view sorts a report into what every renderer shows: headline
// facts, flagged checks biggest first, and the checks that found nothing,
// were skipped or failed, plus the reclaimable totals.
package view

import (
	"sort"
	"strings"

	"tidy/pkg/report"
)

// Group is a titled set of results, e.g. all results of one plugin.
type Group struct {
	Name    string
	Results []report.Result
}

// Overview is a report sorted for display.
type Overview struct {
	Headlines []string        // e.g. "/ 89% full, 24G free"
	Flagged   []report.Result // biggest total size first
	OK        []report.Result
	Skipped   []report.Result
	Errors    []report.Result
	Safe      int64 // bytes marked safe, each place on disk counted once
	Review    int64 // bytes marked review, likewise
}

// Build sorts groups' results. Results whose ids are in headline are
// summarised as Headlines instead of being listed.
func Build(groups []Group, headline []string) Overview {
	head := map[string]bool{}
	for _, id := range headline {
		head[id] = true
	}
	var o Overview
	var all []report.Result
	for _, g := range groups {
		for _, r := range g.Results {
			all = append(all, r)
			switch {
			case head[r.ID] && r.Status == report.StatusFlag:
				for _, it := range r.Items {
					o.Headlines = append(o.Headlines, strings.TrimSpace(it.Label+" "+it.Detail))
				}
			case head[r.ID] && r.Status == report.StatusOK:
			case r.Status == report.StatusFlag:
				o.Flagged = append(o.Flagged, r)
			case r.Status == report.StatusOK:
				o.OK = append(o.OK, r)
			case r.Status == report.StatusSkipped:
				o.Skipped = append(o.Skipped, r)
			case r.Status == report.StatusError:
				o.Errors = append(o.Errors, r)
			}
		}
	}
	// Biggest first; checks with no sizes keep their report order after them.
	sort.SliceStable(o.Flagged, func(i, j int) bool { return o.Flagged[i].TotalSize() > o.Flagged[j].TotalSize() })
	o.Safe = report.Total(all, report.ConfidenceSafe)
	o.Review = report.Total(all, report.ConfidenceReview)
	return o
}
