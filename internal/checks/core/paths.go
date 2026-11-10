package core

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"tidy/internal/engine"
	"tidy/internal/fsx"
	"tidy/pkg/manifest"
	"tidy/pkg/report"
)

// Paths provides paths.scan, the general "files matching X, older than N
// days, bigger than M" check, and the presets built on it.
func Paths() *engine.Plugin {
	return &engine.Plugin{
		Manifest: manifest.Manifest{
			ID: "paths", Name: "Files", Version: "0.1.0", Protocol: manifest.ProtocolVersion,
			Checks: []manifest.CheckSpec{{
				ID:          "scan",
				Title:       "Path scan",
				Description: "Find files or folders matching patterns, age and size rules.",
				Template:    true,
				Hint:        "move one to the trash: gio trash <path>",
				Options: map[string]manifest.Option{
					"paths": {Type: manifest.TypeStrings, Default: []string{},
						Description: "Folders to scan. Supports ~, $VARS and xdg:DOWNLOAD style names"},
					"mode": {Type: manifest.TypeString, Default: "entries", Enum: []string{"entries", "files"},
						Description: "entries: judge each direct child as a whole. files: every file at any depth"},
					"type": {Type: manifest.TypeString, Default: "any", Enum: []string{"any", "dir", "file"},
						Description: "In entries mode, only consider folders or files"},
					"patterns": {Type: manifest.TypeStrings, Default: []string{},
						Description: "Name globs to include, e.g. *.iso. Empty means everything"},
					"exclude": {Type: manifest.TypeStrings, Default: []string{},
						Description: "Name globs to skip (also skips folders while walking)"},
					"min_age_days": {Type: manifest.TypeInteger, Default: 0, Min: manifest.Ptr(0),
						Description: "Only flag things not changed for this many days (a folder's age is its newest file)"},
					"min_size_mb": {Type: manifest.TypeInteger, Default: 0, Min: manifest.Ptr(0),
						Description: "Only flag things at least this big"},
					"owned_by_me": {Type: manifest.TypeBoolean, Default: false,
						Description: "Only flag things owned by the current user"},
					"confidence": {Type: manifest.TypeString, Default: "review", Enum: []string{"safe", "review", "manual"},
						Description: "How safe the found items are to remove"},
				},
			}},
		},
		Checks: map[string]engine.CheckFunc{"scan": pathScan},
		Presets: []engine.Preset{
			{ID: "downloads", Check: "scan", Title: "Old downloads", Options: map[string]any{
				"paths": []string{"xdg:DOWNLOAD"}, "min_age_days": 90, "confidence": "review"}},
			{ID: "user-cache", Check: "scan", Title: "Large caches",
				Hint: "apps rebuild their cache when needed; clearing one frees space until then",
				Options: map[string]any{
					"paths": []string{"~/.cache"}, "min_size_mb": 200, "confidence": "safe"}},
			{ID: "tmp", Check: "scan", Title: "Old temporary files", Options: map[string]any{
				"paths": []string{"/tmp", "/var/tmp"}, "min_age_days": 7, "owned_by_me": true, "confidence": "safe"}},
			{ID: "stale-app-config", Check: "scan", Title: "App data untouched for a year",
				Hint: "a folder here may belong to an app you removed; check before deleting",
				Options: map[string]any{
					"paths": []string{"~/.config", "~/.local/share"}, "type": "dir", "min_age_days": 365,
					"exclude":    []string{"Trash", "applications", "autostart", "fonts", "icons", "keyrings"},
					"confidence": "manual"}},
			{ID: "large-files", Check: "scan", Title: "Very large files", Options: map[string]any{
				"paths": []string{"~"}, "mode": "files", "min_size_mb": 1024,
				"exclude":    []string{".cache", "node_modules", ".git", "Trash", ".local/share/containers", "snap"},
				"confidence": "review"}},
		},
	}
}

type found struct {
	path  string
	size  int64
	mtime time.Time
}

func pathScan(ctx context.Context, in engine.Input) (engine.Outcome, error) {
	o := in.Options
	roots := o.Strings("paths")
	if len(roots) == 0 {
		return engine.Outcome{}, errors.New("no paths set; add paths = [\"...\"] to this check's config")
	}
	r := scanRules{
		patterns: o.Strings("patterns"),
		exclude:  o.Strings("exclude"),
		typ:      o.String("type"),
		minSize:  int64(o.Int("min_size_mb")) << 20,
		ownedBy:  -1,
	}
	if days := o.Int("min_age_days"); days > 0 {
		r.cutoff = time.Now().AddDate(0, 0, -days)
	}
	if o.Bool("owned_by_me") {
		r.ownedBy = os.Getuid()
	}

	var all []found
	var notes []string
	for _, root := range roots {
		abs := fsx.Expand(root)
		if _, err := os.Stat(abs); err != nil {
			notes = append(notes, fmt.Sprintf("%s does not exist, skipped", fsx.Pretty(abs)))
			continue
		}
		var got []found
		var err error
		if o.String("mode") == "files" {
			got, err = scanFiles(ctx, abs, r)
		} else {
			got, err = scanEntries(ctx, abs, r)
		}
		if err != nil {
			return engine.Outcome{}, err
		}
		all = append(all, got...)
	}

	sort.Slice(all, func(i, j int) bool { return all[i].size > all[j].size })
	var total int64
	for _, f := range all {
		total += f.size
	}
	out := engine.Outcome{Notes: notes}
	if len(all) == 0 {
		return out, nil
	}
	out.Summary = fmt.Sprintf("%d found, %s", len(all), fsx.Human(total))
	conf := report.Confidence(o.String("confidence"))
	for _, f := range all {
		out.Items = append(out.Items, report.Item{
			ID: f.path, Label: fsx.Pretty(f.path), Path: f.path, Size: f.size,
			Detail:     "changed " + fsx.Age(f.mtime),
			Confidence: conf,
			Action:     &report.Action{Command: "gio trash " + shellQuote(f.path)},
		})
	}
	return out, nil
}

type scanRules struct {
	patterns, exclude []string
	typ               string
	minSize           int64
	cutoff            time.Time // zero = no age rule
	ownedBy           int       // -1 = anyone
}

func (r scanRules) nameOK(name string) bool {
	if matchAny(r.exclude, name) {
		return false
	}
	return len(r.patterns) == 0 || matchAny(r.patterns, name)
}

func (r scanRules) keep(size int64, newest time.Time, owner uint32) bool {
	if size < r.minSize {
		return false
	}
	if !r.cutoff.IsZero() && newest.After(r.cutoff) {
		return false
	}
	return r.ownedBy < 0 || int(owner) == r.ownedBy
}

// scanEntries judges each direct child of root as a whole: its size is the
// whole tree and its age is the newest change anywhere inside it.
func scanEntries(ctx context.Context, root string, r scanRules) ([]found, error) {
	ents, err := os.ReadDir(root)
	if err != nil {
		return nil, nil // unreadable root: nothing to report
	}
	var out []found
	for _, e := range ents {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if !r.nameOK(e.Name()) {
			continue
		}
		if (r.typ == "dir" && !e.IsDir()) || (r.typ == "file" && e.IsDir()) {
			continue
		}
		p := filepath.Join(root, e.Name())
		st, err := fsx.TreeStat(ctx, p)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			continue
		}
		if r.keep(st.Size, st.Newest, st.Owner) {
			out = append(out, found{path: p, size: st.Size, mtime: st.Newest})
		}
	}
	return out, nil
}

// scanFiles walks root and judges every regular file on its own. It stays
// on root's filesystem and skips excluded names (files or folders), and
// exclude entries containing a slash are matched against the path relative
// to root (e.g. ".local/share/containers").
func scanFiles(ctx context.Context, root string, r scanRules) ([]found, error) {
	var rootDev uint64
	if fi, err := os.Stat(root); err == nil {
		if s, ok := fi.Sys().(*syscall.Stat_t); ok {
			rootDev = uint64(s.Dev)
		}
	}
	var out []found
	n := 0
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if n++; n%512 == 0 && ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if p == root {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		if matchAny(r.exclude, d.Name()) || (strings.Contains(rel, "/") && matchAny(r.exclude, rel)) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if fi, err := d.Info(); err == nil {
				if s, ok := fi.Sys().(*syscall.Stat_t); ok && rootDev != 0 && uint64(s.Dev) != rootDev {
					return fs.SkipDir
				}
			}
			return nil
		}
		if !d.Type().IsRegular() || (len(r.patterns) > 0 && !matchAny(r.patterns, d.Name())) {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return nil
		}
		var owner uint32
		size := fi.Size()
		if s, ok := fi.Sys().(*syscall.Stat_t); ok {
			owner = s.Uid
			size = s.Blocks * 512
		}
		if r.keep(size, fi.ModTime(), owner) {
			out = append(out, found{path: p, size: size, mtime: fi.ModTime()})
		}
		return nil
	})
	if err != nil && ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return out, nil
}

// shellQuote quotes s for a POSIX shell.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
