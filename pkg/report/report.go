// Package report defines the data every tidy surface reads: the results of a
// run and the events streamed while it happens. It is the stable contract
// between the engine, renderers, plugins and any future GUI.
package report

import (
	"path/filepath"
	"time"
)

// SchemaVersion is bumped whenever a field changes meaning or is removed.
const SchemaVersion = 1

// Status is the outcome of one check run.
type Status string

const (
	StatusOK      Status = "ok"      // ran, found nothing to report
	StatusFlag    Status = "flag"    // ran, found items worth a look
	StatusSkipped Status = "skipped" // did not run, see SkipReason
	StatusError   Status = "error"   // tried to run and failed, see Error
)

// Confidence says how sure tidy is that an item is safe to remove.
type Confidence string

const (
	ConfidenceSafe   Confidence = "safe"   // regenerated or clearly unused
	ConfidenceReview Confidence = "review" // probably unused, may hold data
	ConfidenceManual Confidence = "manual" // needs a person to decide
)

// Action is the command that would remove or fix an item. tidy only shows it.
type Action struct {
	Command   string `json:"command"`
	NeedsRoot bool   `json:"needs_root,omitempty"`
}

// Item is one thing a check found.
type Item struct {
	ID         string     `json:"id"`             // stable within its check
	Label      string     `json:"label"`          // what a person reads
	Path       string     `json:"path,omitempty"` // filesystem path, if any
	Size       int64      `json:"size,omitempty"` // bytes removing it would free, 0 if unknown or not applicable
	Detail     string     `json:"detail,omitempty"`
	Confidence Confidence `json:"confidence"`
	Action     *Action    `json:"action,omitempty"`
}

// Result is the outcome of one check instance.
type Result struct {
	ID         string        `json:"id"`     // instance id, e.g. "docker.unused-images"
	Plugin     string        `json:"plugin"` // e.g. "docker"
	Check      string        `json:"check"`  // e.g. "unused-images"
	Title      string        `json:"title"`
	Status     Status        `json:"status"`
	Summary    string        `json:"summary,omitempty"`
	Items      []Item        `json:"items,omitempty"`
	Notes      []string      `json:"notes,omitempty"` // informational lines
	Debug      string        `json:"debug,omitempty"` // diagnostic output, e.g. a plugin's stderr
	Hint       string        `json:"hint,omitempty"`
	SkipReason string        `json:"skip_reason,omitempty"`
	Error      string        `json:"error,omitempty"`
	Duration   time.Duration `json:"duration_ns"`
}

// TotalSize sums the known sizes of the result's items.
func (r *Result) TotalSize() int64 {
	var n int64
	for _, it := range r.Items {
		n += it.Size
	}
	return n
}

// EventType names what happened in the run.
type EventType string

const (
	EventStarted  EventType = "started"  // a check instance began
	EventFinished EventType = "finished" // a check instance produced a Result
)

// Event is streamed by the engine while a run is in progress.
type Event struct {
	Type   EventType `json:"type"`
	ID     string    `json:"id"`
	Time   time.Time `json:"time"`
	Result *Result   `json:"result,omitempty"` // set on EventFinished
}

// Report is a whole run, collected from its events.
type Report struct {
	SchemaVersion int       `json:"schema_version"`
	Host          string    `json:"host"`
	StartedAt     time.Time `json:"started_at"`
	FinishedAt    time.Time `json:"finished_at"`
	Results       []Result  `json:"results"`
}

// SafeTotal sums the sizes of items marked safe; see Total.
func SafeTotal(results []Result) int64 { return Total(results, ConfidenceSafe) }

// Total sums the sizes of items with the given confidence across results,
// counting each place on disk once: an item whose path is the same as, or
// inside, another counted item's path is not added again. Different checks
// often look at the same folders (e.g. ~/.cache), so a plain sum
// overstates. Overlaps that are not nested paths cannot be detected, so
// treat the result as an upper estimate.
func Total(results []Result, conf Confidence) int64 {
	sizes := map[string]int64{} // path -> largest size reported for it
	var total int64
	for _, r := range results {
		for _, it := range r.Items {
			if it.Confidence != conf {
				continue
			}
			if it.Path == "" {
				total += it.Size
				continue
			}
			p := filepath.Clean(it.Path)
			sizes[p] = max(sizes[p], it.Size)
		}
	}
	for p, size := range sizes {
		if !insideAny(p, sizes) {
			total += size
		}
	}
	return total
}

// Caution returns the least safe confidence among the result's items:
// manual over review over safe. It is "" when there are no items.
func (r *Result) Caution() Confidence {
	rank := map[Confidence]int{ConfidenceSafe: 1, ConfidenceReview: 2, ConfidenceManual: 3}
	var worst Confidence
	for _, it := range r.Items {
		if rank[it.Confidence] > rank[worst] {
			worst = it.Confidence
		}
	}
	return worst
}

// insideAny reports whether any parent folder of p is a key in paths.
func insideAny(p string, paths map[string]int64) bool {
	for dir := filepath.Dir(p); ; dir = filepath.Dir(dir) {
		if _, ok := paths[dir]; ok {
			return true
		}
		if dir == "/" || dir == "." {
			return false
		}
	}
}
