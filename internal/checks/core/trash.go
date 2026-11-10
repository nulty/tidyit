package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"tidy/internal/engine"
	"tidy/internal/fsx"
	"tidy/pkg/manifest"
	"tidy/pkg/report"
)

// Trash reports the size of the user's freedesktop trash.
func Trash() *engine.Plugin {
	return &engine.Plugin{
		Manifest: manifest.Manifest{
			ID: "trash", Name: "Trash", Version: "0.1.0", Protocol: manifest.ProtocolVersion,
			Checks: []manifest.CheckSpec{{
				ID:    "size",
				Title: "Trash",
				Hint:  "empty it: gio trash --empty",
				Options: map[string]manifest.Option{
					"min_size_mb": {Type: manifest.TypeInteger, Default: 100, Min: manifest.Ptr(0),
						Description: "Flag the trash when it holds at least this much"},
				},
			}},
		},
		Checks: map[string]engine.CheckFunc{"size": trashSize},
	}
}

func trashSize(ctx context.Context, in engine.Input) (engine.Outcome, error) {
	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" {
		dataHome = filepath.Join(fsx.Home(), ".local", "share")
	}
	dir := filepath.Join(dataHome, "Trash", "files")
	ents, err := os.ReadDir(dir)
	if err != nil {
		return engine.Outcome{Summary: "empty"}, nil
	}
	st, err := fsx.TreeStat(ctx, dir)
	if err != nil {
		return engine.Outcome{}, err
	}
	desc := fmt.Sprintf("%d items, %s", len(ents), fsx.Human(st.Size))
	if len(ents) == 0 || st.Size < int64(in.Options.Int("min_size_mb"))<<20 {
		return engine.Outcome{Summary: desc}, nil
	}
	return engine.Outcome{
		Summary: desc,
		Items: []report.Item{{
			ID: "trash", Label: fmt.Sprintf("Trash (%d items)", len(ents)), Path: dir, Size: st.Size,
			Confidence: report.ConfidenceSafe,
			Action:     &report.Action{Command: "gio trash --empty"},
		}},
	}, nil
}
