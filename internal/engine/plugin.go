// Package engine discovers check instances, runs them in parallel and streams
// their results as events. It knows nothing about how results are shown.
package engine

import (
	"context"
	"errors"
	"fmt"

	"tidy/pkg/manifest"
	"tidy/pkg/report"
)

// Input is what a check function receives.
type Input struct {
	ID      string          // instance id
	Options manifest.Values // resolved and validated
}

// Outcome is what a check function returns. The engine turns it into a
// report.Result, deriving the status from whether any items were found.
type Outcome struct {
	Items   []report.Item
	Summary string
	Notes   []string
	Hint    string // overrides the spec's hint when set
}

// CheckFunc runs one check. It must stop promptly when ctx is cancelled.
type CheckFunc func(ctx context.Context, in Input) (Outcome, error)

// Preset is a ready-made instance of a template check, such as the
// "downloads" instance of paths.scan.
type Preset struct {
	ID      string
	Check   string
	Title   string
	Hint    string
	Options map[string]any
}

// Plugin is a manifest plus the code that runs its checks.
type Plugin struct {
	Manifest manifest.Manifest
	Checks   map[string]CheckFunc
	Presets  []Preset
	// Probe optionally checks the plugin can run at all (e.g. the docker
	// daemon is reachable). Return Skip(...) to skip all its checks.
	Probe func(ctx context.Context) error
}

// SkipError marks a check, or a whole plugin, as not applicable here.
type SkipError struct{ Reason string }

func (e *SkipError) Error() string { return "skipped: " + e.Reason }

// Skip returns an error that makes the engine report the check as skipped.
func Skip(format string, args ...any) error {
	return &SkipError{Reason: fmt.Sprintf(format, args...)}
}

func asSkip(err error) (*SkipError, bool) {
	var s *SkipError
	ok := errors.As(err, &s)
	return s, ok
}
