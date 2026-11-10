package core

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"tidyit/internal/engine"
	"tidyit/internal/fsx"
	"tidyit/pkg/manifest"
	"tidyit/pkg/report"
)

// Autostart reports login items (XDG Autostart) whose program is missing.
func Autostart() *engine.Plugin {
	return &engine.Plugin{
		Manifest: manifest.Manifest{
			ID: "autostart", Name: "Autostart", Version: "0.1.0", Protocol: manifest.ProtocolVersion,
			Checks: []manifest.CheckSpec{{
				ID:    "broken",
				Title: "Login items",
				Hint:  "remove your own entries; hide a system one for you only by copying it to ~/.config/autostart and adding Hidden=true",
				Options: map[string]manifest.Option{
					"include_system": {Type: manifest.TypeBoolean, Default: true,
						Description: "Also check system-wide entries in /etc/xdg/autostart"},
				},
			}},
		},
		Checks: map[string]engine.CheckFunc{"broken": autostartBroken},
	}
}

type desktopEntry struct {
	path, file      string
	name            string
	exec, tryExec   string
	hidden, enabled bool
	user            bool
}

func autostartBroken(_ context.Context, in engine.Input) (engine.Outcome, error) {
	userDir := filepath.Join(configHome(), "autostart")
	entries := map[string]*desktopEntry{} // by file name; user overrides system

	if in.Options.Bool("include_system") {
		dirs := os.Getenv("XDG_CONFIG_DIRS")
		if dirs == "" {
			dirs = "/etc/xdg"
		}
		// Earlier dirs take precedence, so read them last.
		list := strings.Split(dirs, ":")
		for i := len(list) - 1; i >= 0; i-- {
			loadAutostart(filepath.Join(list[i], "autostart"), false, entries)
		}
	}
	loadAutostart(userDir, true, entries)

	var out engine.Outcome
	var userItems []string
	for _, file := range sortedKeys(entries) {
		e := entries[file]
		if e.user {
			userItems = append(userItems, strings.TrimSuffix(file, ".desktop"))
		}
		if e.hidden || !e.enabled {
			continue
		}
		// Per the spec, a missing TryExec means "ignore this entry". That is
		// normal for system entries (e.g. for a desktop you don't use), but a
		// user entry like that is a leftover from a removed app.
		if e.tryExec != "" && !programExists(e.tryExec) {
			if e.user {
				out.Items = append(out.Items, autostartItem(e, e.tryExec))
			}
			continue
		}
		prog := execProgram(e.exec)
		if prog != "" && !programExists(prog) {
			out.Items = append(out.Items, autostartItem(e, prog))
		}
	}
	if len(userItems) > 0 {
		out.Notes = append(out.Notes, "your login items: "+strings.Join(userItems, ", "))
	}
	if len(out.Items) > 0 {
		out.Summary = "they start programs that are not installed"
	}
	return out, nil
}

func autostartItem(e *desktopEntry, prog string) report.Item {
	label := e.file
	if e.name != "" {
		label = fmt.Sprintf("%s (%s)", e.name, e.file)
	}
	it := report.Item{
		ID: e.path, Label: label, Path: e.path,
		Detail: fmt.Sprintf("runs %s, which is not installed", prog),
	}
	if e.user {
		it.Confidence = report.ConfidenceSafe
		it.Action = &report.Action{Command: "gio trash " + shellQuote(e.path)}
	} else {
		it.Confidence = report.ConfidenceManual
		it.Detail += " (system entry, owned by a package)"
	}
	return it
}

func loadAutostart(dir string, user bool, into map[string]*desktopEntry) {
	files, _ := filepath.Glob(filepath.Join(dir, "*.desktop"))
	for _, p := range files {
		e, err := parseDesktop(p)
		if err != nil {
			continue
		}
		e.user = user
		into[e.file] = e
	}
}

// parseDesktop reads the keys tidyit needs from a .desktop file's
// [Desktop Entry] group.
func parseDesktop(path string) (*desktopEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	e := &desktopEntry{path: path, file: filepath.Base(path), enabled: true}
	inMain := false
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "[") {
			inMain = line == "[Desktop Entry]"
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !inMain || !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		switch k {
		case "Name":
			e.name = v
		case "Exec":
			e.exec = v
		case "TryExec":
			e.tryExec = v
		case "Hidden":
			e.hidden = strings.EqualFold(v, "true")
		case "X-GNOME-Autostart-enabled":
			e.enabled = !strings.EqualFold(v, "false")
		}
	}
	return e, sc.Err()
}

// execProgram returns the program an Exec line runs, skipping a leading
// `env VAR=value ...` and handling simple double quotes.
func execProgram(execLine string) string {
	toks := splitExec(execLine)
	i := 0
	if i < len(toks) && filepath.Base(toks[i]) == "env" {
		i++
		for i < len(toks) && (strings.Contains(toks[i], "=") || strings.HasPrefix(toks[i], "-")) {
			switch toks[i] {
			case "-u", "--unset", "-C", "--chdir", "-S", "--split-string":
				i++ // these take a separate argument
			}
			i++
		}
	}
	if i < len(toks) {
		return toks[i]
	}
	return ""
}

func splitExec(s string) []string {
	var toks []string
	var cur strings.Builder
	inQuote, has := false, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\\' && inQuote && i+1 < len(s):
			i++
			cur.WriteByte(s[i])
		case c == '"':
			inQuote, has = !inQuote, true
		case (c == ' ' || c == '\t') && !inQuote:
			if has || cur.Len() > 0 {
				toks = append(toks, cur.String())
				cur.Reset()
				has = false
			}
		default:
			cur.WriteByte(c)
		}
	}
	if has || cur.Len() > 0 {
		toks = append(toks, cur.String())
	}
	return toks
}

func programExists(prog string) bool {
	if strings.Contains(prog, "/") {
		fi, err := os.Stat(prog)
		return err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0
	}
	_, err := exec.LookPath(prog)
	return err == nil
}

func configHome() string {
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return d
	}
	return filepath.Join(fsx.Home(), ".config")
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
