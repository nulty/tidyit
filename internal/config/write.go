package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// Header starts every config file Tidy It writes. A file without it was
// written by hand, so Update keeps a backup before replacing it.
const Header = "# Managed by Tidy It."

const headerText = Header + ` Changing settings in the app rewrites this file,
# so comments and layout are not kept. Editing it by hand still works.
`

// SetOption sets an option override for a check id. A nil value removes it.
func (c *Config) SetOption(id, name string, value any) {
	if value == nil {
		if e := c.Checks[id]; e != nil {
			delete(e.Options, name)
			c.prune(id)
		}
		return
	}
	c.entry(id).Options[name] = value
}

// SetEnabled turns a check id on or off. A nil value removes the setting,
// so the check falls back to its default (or a prefix entry's setting).
func (c *Config) SetEnabled(id string, enabled *bool) {
	if enabled == nil {
		if e := c.Checks[id]; e != nil {
			e.Enabled = nil
			c.prune(id)
		}
		return
	}
	v := *enabled
	c.entry(id).Enabled = &v
}

// Reset removes a check id's overrides: enabled, timeout and options. A
// user-defined instance keeps its use and title, so it still exists.
func (c *Config) Reset(id string) {
	if e := c.Checks[id]; e != nil {
		e.Enabled, e.Timeout, e.Options = nil, 0, map[string]any{}
		c.prune(id)
	}
}

func (c *Config) entry(id string) *Entry {
	if c.Checks == nil {
		c.Checks = map[string]*Entry{}
	}
	e := c.Checks[id]
	if e == nil {
		e = &Entry{}
		c.Checks[id] = e
	}
	if e.Options == nil {
		e.Options = map[string]any{}
	}
	return e
}

// prune drops an entry that no longer sets anything.
func (c *Config) prune(id string) {
	e := c.Checks[id]
	if e != nil && e.Use == "" && e.Title == "" && e.Enabled == nil && e.Timeout == 0 && len(e.Options) == 0 {
		delete(c.Checks, id)
	}
}

// Marshal renders the config as TOML, starting with Header. Checks are
// written in id order as [checks.<id>] tables.
func (c *Config) Marshal() ([]byte, error) {
	root := map[string]any{}
	run := map[string]any{}
	if c.Run.Concurrency > 0 {
		run["concurrency"] = c.Run.Concurrency
	}
	if c.Run.Timeout > 0 {
		run["timeout"] = c.Run.Timeout.String()
	}
	if len(run) > 0 {
		root["run"] = run
	}
	checks := map[string]any{}
	for _, id := range c.IDs() {
		e := c.Checks[id]
		tbl, err := table(checks, id)
		if err != nil {
			return nil, err
		}
		if e.Use != "" {
			tbl["use"] = e.Use
		}
		if e.Title != "" {
			tbl["title"] = e.Title
		}
		if e.Enabled != nil {
			tbl["enabled"] = *e.Enabled
		}
		if e.Timeout > 0 {
			tbl["timeout"] = e.Timeout.String()
		}
		for k, v := range e.Options {
			if _, clash := tbl[k]; clash {
				return nil, fmt.Errorf("[checks.%s]: option %q clashes with check id %s.%s", id, k, id, k)
			}
			tbl[k] = v
		}
	}
	if len(checks) > 0 {
		root["checks"] = checks
	}
	var buf bytes.Buffer
	buf.WriteString(headerText)
	if len(root) > 0 {
		buf.WriteString("\n")
		enc := toml.NewEncoder(&buf)
		enc.Indent = ""
		if err := enc.Encode(root); err != nil {
			return nil, err
		}
	}
	return buf.Bytes(), nil
}

// table returns the nested table for a dotted check id, creating it. Ids
// are dotted paths, so [checks.docker.unused-images] sits inside
// [checks.docker], exactly as Load reads it back.
func table(root map[string]any, id string) (map[string]any, error) {
	cur := root
	for _, part := range strings.Split(id, ".") {
		switch next := cur[part].(type) {
		case nil:
			m := map[string]any{}
			cur[part] = m
			cur = m
		case map[string]any:
			cur = next
		default:
			return nil, fmt.Errorf("check id %q clashes with option %q", id, part)
		}
	}
	return cur, nil
}

// Update loads the config at path (a missing file is an empty config),
// lets change edit it, and writes the result back. Reading again on every
// change means edits made meanwhile in a text editor are kept.
//
// If the file exists and was not written by Tidy It (no Header), it is
// first copied to a backup, whose path is returned; otherwise backup is
// "". The new file replaces the old one atomically. If change returns an
// error, nothing is written.
func Update(path string, change func(*Config) error) (backup string, err error) {
	if path == "" {
		return "", errors.New("no config path (is $HOME set?)")
	}
	// Write through a symlink (e.g. into a dotfiles repo) rather than
	// replacing the link with a file.
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real
	}
	old, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	cfg, err := Load(path, false)
	if err != nil {
		return "", err
	}
	if err := change(cfg); err != nil {
		return "", err
	}
	data, err := cfg.Marshal()
	if err != nil {
		return "", err
	}
	if bytes.Equal(data, old) {
		return "", nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	mode := fs.FileMode(0o644)
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	}
	if len(bytes.TrimSpace(old)) > 0 && !bytes.HasPrefix(old, []byte(Header)) {
		if backup, err = freeName(path + ".bak"); err != nil {
			return "", err
		}
		if err := os.WriteFile(backup, old, mode); err != nil {
			return "", fmt.Errorf("backing up %s: %w", path, err)
		}
	}
	if err := writeAtomic(path, data, mode); err != nil {
		return backup, err
	}
	return backup, nil
}

// freeName returns name, or name.2, name.3 ... if it is taken, so an
// earlier backup is never overwritten.
func freeName(name string) (string, error) {
	for i := 1; i < 1000; i++ {
		try := name
		if i > 1 {
			try = fmt.Sprintf("%s.%d", name, i)
		}
		if _, err := os.Lstat(try); errors.Is(err, fs.ErrNotExist) {
			return try, nil
		}
	}
	return "", fmt.Errorf("too many backups named %s.*", name)
}

func writeAtomic(path string, data []byte, mode fs.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".config-*.toml")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp) // no-op after a successful rename
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Chmod(mode); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Inherited returns the enabled setting id gets from its nearest prefix
// entry, e.g. [checks.docker] for docker.unused-images, ignoring id's own
// entry. It is nil when no prefix sets one.
func (c *Config) Inherited(id string) *bool {
	for p := id; ; {
		i := strings.LastIndex(p, ".")
		if i < 0 {
			return nil
		}
		p = p[:i]
		if e := c.Checks[p]; e != nil && e.Enabled != nil {
			v := *e.Enabled
			return &v
		}
	}
}
