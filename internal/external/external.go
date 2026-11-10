// Package external loads plugins that live outside tidyit: a folder holding a
// plugin.toml manifest and an executable entrypoint, in any language.
//
// Protocol 1. For each check instance tidyit runs the entrypoint once, in the
// plugin's folder, and writes one JSON object to its stdin:
//
//	{"protocol": 1, "check": "largest", "instance": "home-usage.largest",
//	 "options": {"count": 10}}
//
// Options are already validated and filled with defaults. The plugin
// writes JSON lines to stdout, one message per line:
//
//	{"type": "item", "id": "...", "label": "...", "path": "...", "size": 123,
//	 "detail": "...", "confidence": "safe|review|manual",
//	 "action": {"command": "...", "needs_root": false}}
//	{"type": "note", "text": "..."}
//	{"type": "summary", "text": "...", "hint": "..."}
//	{"type": "skip", "reason": "..."}
//
// Items make the result "flag"; none make it "ok"; a skip makes it
// "skipped" (and nothing else may be sent). A non-zero exit, a line that
// isn't a known message or a timeout makes it "error". stderr is kept as
// debug output. Items with no confidence default to "review".
package external

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/BurntSushi/toml"

	"tidyit/internal/engine"
	"tidyit/pkg/manifest"
	"tidyit/pkg/report"
)

// ManifestFile is the manifest's name inside a plugin folder.
const ManifestFile = "plugin.toml"

// Limits on what a plugin may send, so a runaway script can't exhaust memory.
const (
	maxLine   = 1 << 20  // one stdout line
	maxStdout = 16 << 20 // all of stdout
	maxStderr = 64 << 10 // kept from stderr (the tail)
)

// Dirs returns where tidyit looks for plugins, first match winning: each
// entry of $TIDYIT_PLUGIN_PATH (colon separated), then
// $XDG_DATA_HOME/tidyit/plugins, then tidyit/plugins under each entry of
// $XDG_DATA_DIRS (default /usr/local/share:/usr/share), where packages
// install them.
func Dirs() []string {
	var dirs []string
	for _, d := range filepath.SplitList(os.Getenv("TIDYIT_PLUGIN_PATH")) {
		if d != "" {
			dirs = append(dirs, d)
		}
	}
	data := os.Getenv("XDG_DATA_HOME")
	if data == "" {
		if h, err := os.UserHomeDir(); err == nil {
			data = filepath.Join(h, ".local", "share")
		}
	}
	if data != "" {
		dirs = append(dirs, filepath.Join(data, "tidyit", "plugins"))
	}
	system := os.Getenv("XDG_DATA_DIRS")
	if system == "" {
		system = "/usr/local/share:/usr/share"
	}
	for _, d := range filepath.SplitList(system) {
		if d != "" {
			dirs = append(dirs, filepath.Join(d, "tidyit", "plugins"))
		}
	}
	return dirs
}

// LoadAll loads every plugin folder under dirs, in name order. A folder
// that fails to load gives a warning, not an error, so one broken plugin
// doesn't stop the run. Missing dirs are ignored. A plugin whose id an
// earlier dir already provided is skipped quietly, so a user's copy
// overrides an installed one; the same id twice in one dir is a warning.
func LoadAll(dirs []string) ([]*engine.Plugin, []string) {
	var plugins []*engine.Plugin
	var warnings []string
	seen := map[string]string{} // plugin id -> its folder
	fromDir := map[string]int{} // plugin id -> index of the dir it came from
	for i, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				warnings = append(warnings, fmt.Sprintf("plugins: %v", err))
			}
			continue
		}
		for _, e := range entries {
			pdir := filepath.Join(dir, e.Name())
			if fi, err := os.Stat(pdir); err != nil || !fi.IsDir() {
				continue
			}
			if _, err := os.Stat(filepath.Join(pdir, ManifestFile)); err != nil {
				continue // not a plugin folder
			}
			p, err := Load(pdir)
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("plugin %s: %v", pdir, err))
				continue
			}
			if prev, dup := seen[p.Manifest.ID]; dup {
				if fromDir[p.Manifest.ID] == i {
					warnings = append(warnings, fmt.Sprintf("plugin %s: id %q is already loaded from %s; skipping", pdir, p.Manifest.ID, prev))
				}
				continue
			}
			seen[p.Manifest.ID] = pdir
			fromDir[p.Manifest.ID] = i
			plugins = append(plugins, p)
		}
	}
	return plugins, warnings
}

// Load reads and validates one plugin folder.
func Load(dir string) (*engine.Plugin, error) {
	var m manifest.Manifest
	md, err := toml.DecodeFile(filepath.Join(dir, ManifestFile), &m)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", ManifestFile, err)
	}
	if und := md.Undecoded(); len(und) > 0 {
		keys := make([]string, len(und))
		for i, k := range und {
			keys[i] = k.String()
		}
		sort.Strings(keys)
		return nil, fmt.Errorf("%s: unknown keys: %s", ManifestFile, strings.Join(keys, ", "))
	}
	if err := m.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", ManifestFile, err)
	}
	if m.Name == "" {
		m.Name = m.ID
	}
	if m.Entrypoint == "" {
		m.Entrypoint = "run"
	}
	entry := filepath.Join(dir, m.Entrypoint)
	if rel, err := filepath.Rel(dir, entry); err != nil || strings.HasPrefix(rel, "..") {
		return nil, fmt.Errorf("entrypoint %q must be inside the plugin folder", m.Entrypoint)
	}
	fi, err := os.Stat(entry)
	if err != nil {
		return nil, fmt.Errorf("entrypoint: %w", err)
	}
	if fi.IsDir() || fi.Mode().Perm()&0o111 == 0 {
		return nil, fmt.Errorf("entrypoint %s is not an executable file (chmod +x it)", entry)
	}

	p := &engine.Plugin{Manifest: m, Dir: dir, Checks: map[string]engine.CheckFunc{}}
	for _, c := range m.Checks {
		checkID := c.ID
		p.Checks[checkID] = func(ctx context.Context, in engine.Input) (engine.Outcome, error) {
			return run(ctx, dir, entry, checkID, in)
		}
	}
	return p, nil
}

// request is what tidyit writes to the plugin's stdin.
type request struct {
	Protocol int             `json:"protocol"`
	Check    string          `json:"check"`
	Instance string          `json:"instance"`
	Options  manifest.Values `json:"options"`
}

func run(ctx context.Context, dir, entry, check string, in engine.Input) (engine.Outcome, error) {
	req, err := json.Marshal(request{Protocol: manifest.ProtocolVersion, Check: check, Instance: in.ID, Options: in.Options})
	if err != nil {
		return engine.Outcome{}, err
	}
	cmd := exec.CommandContext(ctx, entry)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("TIDYIT_PROTOCOL=%d", manifest.ProtocolVersion),
		"TIDYIT_PLUGIN_DIR="+dir,
		"TIDYIT_CHECK="+check,
		"TIDYIT_INSTANCE="+in.ID,
	)
	cmd.Stdin = bytes.NewReader(append(req, '\n'))
	// Run in its own process group so a timeout kills anything it started,
	// and don't wait forever for children that keep stdout open.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 2 * time.Second
	stderr := &tailBuffer{max: maxStderr}
	cmd.Stderr = stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return engine.Outcome{}, err
	}
	if err := cmd.Start(); err != nil {
		return engine.Outcome{}, fmt.Errorf("starting %s: %w", entry, err)
	}
	out, perr := parse(io.LimitReader(stdout, maxStdout))
	if perr != nil {
		_ = cmd.Cancel() // stop a plugin we have stopped listening to
	}
	if extra, _ := io.Copy(io.Discard, stdout); extra > 0 && perr == nil {
		perr = fmt.Errorf("more than %d bytes on stdout", maxStdout)
		_ = cmd.Cancel()
	}
	werr := cmd.Wait()
	debug := stderr.String()

	switch {
	case ctx.Err() != nil:
		return engine.Outcome{}, &engine.DebugError{Err: ctx.Err(), Debug: debug}
	case werr != nil && perr == nil:
		msg := fmt.Sprintf("plugin %s", werr)
		if last := lastLine(debug); last != "" {
			msg += ": " + last
		}
		return engine.Outcome{}, &engine.DebugError{Err: errors.New(msg), Debug: debug}
	case perr != nil:
		return engine.Outcome{}, &engine.DebugError{Err: fmt.Errorf("plugin output: %w", perr), Debug: debug}
	}
	if out.skip != "" {
		return engine.Outcome{}, engine.Skip("%s", out.skip)
	}
	out.Debug = debug
	return out.Outcome, nil
}

// message is one stdout line from a plugin.
type message struct {
	Type string `json:"type"`
	// item
	ID         string         `json:"id"`
	Label      string         `json:"label"`
	Path       string         `json:"path"`
	Size       int64          `json:"size"`
	Detail     string         `json:"detail"`
	Confidence string         `json:"confidence"`
	Action     *report.Action `json:"action"`
	// note, summary
	Text string `json:"text"`
	Hint string `json:"hint"`
	// skip
	Reason string `json:"reason"`
}

type parsed struct {
	engine.Outcome
	skip string
}

// parse reads a plugin's JSON lines into an Outcome.
func parse(r io.Reader) (parsed, error) {
	var out parsed
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), maxLine)
	n := 0
	seenIDs := map[string]bool{}
	for sc.Scan() {
		n++
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var m message
		dec := json.NewDecoder(bytes.NewReader(line))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&m); err != nil {
			return out, fmt.Errorf("line %d: %v: %s", n, err, clip(line))
		}
		switch m.Type {
		case "item":
			it := report.Item{ID: m.ID, Label: m.Label, Path: m.Path, Size: m.Size, Detail: m.Detail,
				Confidence: report.Confidence(m.Confidence), Action: m.Action}
			if it.Label == "" {
				it.Label = it.ID
			}
			if it.ID == "" {
				it.ID = it.Label
			}
			if it.ID == "" {
				return out, fmt.Errorf("line %d: item needs an id or a label", n)
			}
			if seenIDs[it.ID] {
				return out, fmt.Errorf("line %d: item id %q sent twice", n, it.ID)
			}
			seenIDs[it.ID] = true
			switch it.Confidence {
			case "":
				it.Confidence = report.ConfidenceReview
			case report.ConfidenceSafe, report.ConfidenceReview, report.ConfidenceManual:
			default:
				return out, fmt.Errorf("line %d: confidence %q must be safe, review or manual", n, m.Confidence)
			}
			if it.Size < 0 {
				return out, fmt.Errorf("line %d: size must not be negative", n)
			}
			if it.Action != nil && it.Action.Command == "" {
				it.Action = nil
			}
			out.Items = append(out.Items, it)
		case "note":
			out.Notes = append(out.Notes, m.Text)
		case "summary":
			out.Summary, out.Hint = m.Text, m.Hint
		case "skip":
			if m.Reason == "" {
				return out, fmt.Errorf("line %d: skip needs a reason", n)
			}
			out.skip = m.Reason
		default:
			return out, fmt.Errorf("line %d: unknown message type %q (want item, note, summary or skip)", n, m.Type)
		}
	}
	if err := sc.Err(); err != nil {
		if errors.Is(err, bufio.ErrTooLong) {
			return out, fmt.Errorf("line %d is longer than %d bytes", n+1, maxLine)
		}
		return out, err
	}
	if out.skip != "" && (len(out.Items) > 0 || len(out.Notes) > 0) {
		return out, fmt.Errorf("sent a skip together with items or notes")
	}
	return out, nil
}

func clip(b []byte) string {
	if len(b) > 120 {
		return string(b[:117]) + "..."
	}
	return string(b)
}

func lastLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		s = s[i+1:]
	}
	if len(s) > 200 {
		s = s[:197] + "..."
	}
	return s
}

// tailBuffer keeps the last max bytes written to it.
type tailBuffer struct {
	buf       []byte
	max       int
	truncated bool
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	if over := len(t.buf) - t.max; over > 0 {
		t.buf = append(t.buf[:0], t.buf[over:]...)
		t.truncated = true
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	if t.truncated {
		return "[earlier output dropped]\n" + string(t.buf)
	}
	return string(t.buf)
}
