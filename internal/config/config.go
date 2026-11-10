// Package config loads the user's tidy config: overrides only, never the
// full set of defaults (those live in each plugin's manifest).
//
//	[run]
//	concurrency = 8
//	timeout = "30s"
//
//	[checks.docker]                 # applies to every docker.* check
//	enabled = true
//
//	[checks.docker.unused-images]   # same as [checks."docker.unused-images"]
//	min_age_days = 60
//
//	[checks.old-isos]               # a new instance of a template check
//	use = "paths.scan"
//	paths = ["~/Downloads"]
//	patterns = ["*.iso"]
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// Entry is the config for one check id (or an id prefix, for `enabled`).
type Entry struct {
	Use     string         // plugin.check, for user-defined instances
	Title   string         // display title override
	Enabled *bool          // nil = not set
	Timeout time.Duration  // 0 = not set
	Options map[string]any // everything else
}

// Run holds settings for the whole run.
type Run struct {
	Concurrency int
	Timeout     time.Duration
}

// Config is the parsed config file.
type Config struct {
	Path   string // where it was loaded from, "" if none
	Run    Run
	Checks map[string]*Entry
}

// DefaultPath is $XDG_CONFIG_HOME/tidy/config.toml.
func DefaultPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "tidy", "config.toml")
}

// Load reads the config at path. A missing file is not an error unless
// required is true; it yields an empty config.
func Load(path string, required bool) (*Config, error) {
	cfg := &Config{Checks: map[string]*Entry{}}
	if path == "" {
		return cfg, nil
	}
	var raw map[string]any
	if _, err := toml.DecodeFile(path, &raw); err != nil {
		if errors.Is(err, fs.ErrNotExist) && !required {
			return cfg, nil
		}
		return nil, fmt.Errorf("config %s: %w", path, err)
	}
	cfg.Path = path
	for key, val := range raw {
		tbl, ok := val.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("config %s: top-level %q must be a table", path, key)
		}
		switch key {
		case "run":
			if err := parseRun(tbl, &cfg.Run); err != nil {
				return nil, fmt.Errorf("config %s: [run]: %w", path, err)
			}
		case "checks":
			if err := flatten("", tbl, cfg.Checks); err != nil {
				return nil, fmt.Errorf("config %s: %w", path, err)
			}
		default:
			return nil, fmt.Errorf("config %s: unknown section [%s] (expected [run] or [checks])", path, key)
		}
	}
	return cfg, nil
}

// IDs returns the configured ids, sorted so prefixes come before the ids
// they contain.
func (c *Config) IDs() []string {
	ids := make([]string, 0, len(c.Checks))
	for id := range c.Checks {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func parseRun(tbl map[string]any, r *Run) error {
	for k, v := range tbl {
		switch k {
		case "concurrency":
			n, ok := v.(int64)
			if !ok || n < 1 {
				return fmt.Errorf("concurrency must be a positive integer")
			}
			r.Concurrency = int(n)
		case "timeout":
			d, err := duration(v)
			if err != nil {
				return fmt.Errorf("timeout: %w", err)
			}
			r.Timeout = d
		default:
			return fmt.Errorf("unknown key %q", k)
		}
	}
	return nil
}

// flatten walks [checks...] tables. Option values are never tables, so a
// nested table always means a longer check id.
func flatten(prefix string, tbl map[string]any, out map[string]*Entry) error {
	var e *Entry
	entry := func() *Entry {
		if e == nil {
			e = &Entry{Options: map[string]any{}}
			out[prefix] = e
		}
		return e
	}
	for k, v := range tbl {
		if sub, ok := v.(map[string]any); ok {
			id := k
			if prefix != "" {
				id = prefix + "." + k
			}
			if err := flatten(id, sub, out); err != nil {
				return err
			}
			continue
		}
		if prefix == "" {
			return fmt.Errorf("[checks]: %q must be a table such as [checks.%s]", k, k)
		}
		switch k {
		case "use":
			s, ok := v.(string)
			if !ok || !strings.Contains(s, ".") {
				return fmt.Errorf("[checks.%s]: use must be \"plugin.check\"", prefix)
			}
			entry().Use = s
		case "title":
			s, ok := v.(string)
			if !ok {
				return fmt.Errorf("[checks.%s]: title must be a string", prefix)
			}
			entry().Title = s
		case "enabled":
			b, ok := v.(bool)
			if !ok {
				return fmt.Errorf("[checks.%s]: enabled must be true or false", prefix)
			}
			entry().Enabled = &b
		case "timeout":
			d, err := duration(v)
			if err != nil {
				return fmt.Errorf("[checks.%s]: timeout: %w", prefix, err)
			}
			entry().Timeout = d
		default:
			entry().Options[k] = v
		}
	}
	return nil
}

func duration(v any) (time.Duration, error) {
	switch x := v.(type) {
	case string:
		d, err := time.ParseDuration(x)
		if err != nil || d <= 0 {
			return 0, fmt.Errorf("want a duration like \"30s\", got %q", x)
		}
		return d, nil
	case int64:
		if x <= 0 {
			return 0, fmt.Errorf("must be positive")
		}
		return time.Duration(x) * time.Second, nil
	}
	return 0, fmt.Errorf("want a duration like \"30s\"")
}
