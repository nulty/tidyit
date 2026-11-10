package snap

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"

	"tidy/internal/engine"
	"tidy/pkg/report"
)

const (
	snapdSocket = "/run/snapd.socket"
	seedFile    = "/var/lib/snapd/seed/seed.yaml"
)

// snapInfo is the part of snapd's /v2/snaps entry tidy reads.
type snapInfo struct {
	Name          string    `json:"name"`
	Type          string    `json:"type"` // app, base, snapd, os, kernel, gadget
	Base          string    `json:"base"`
	Revision      string    `json:"revision"`
	Status        string    `json:"status"`
	InstalledSize int64     `json:"installed-size"`
	InstallDate   time.Time `json:"install-date"`
	Apps          []struct {
		Name string `json:"name"`
	} `json:"apps"`
}

type plugRef struct {
	Snap string `json:"snap"`
	Plug string `json:"plug"`
}

type slotRef struct {
	Snap string `json:"snap"`
	Slot string `json:"slot"`
}

// connections is the part of /v2/connections?select=all tidy reads.
type connections struct {
	Established []struct {
		Plug      plugRef `json:"plug"`
		Slot      slotRef `json:"slot"`
		Interface string  `json:"interface"`
	} `json:"established"`
	Plugs []struct {
		Snap      string         `json:"snap"`
		Interface string         `json:"interface"`
		Attrs     map[string]any `json:"attrs"`
	} `json:"plugs"`
	Slots []struct {
		Snap      string `json:"snap"`
		Interface string `json:"interface"`
	} `json:"slots"`
}

// snapd queries snapd's REST API, which unprivileged users may read.
func snapd(ctx context.Context, path string, out any) error {
	client := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", snapdSocket)
		},
	}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://localhost"+path, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("snapd API: %w", err)
	}
	defer resp.Body.Close()
	var env struct {
		StatusCode int             `json:"status-code"`
		Result     json.RawMessage `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return fmt.Errorf("snapd API %s: %w", path, err)
	}
	if env.StatusCode != http.StatusOK {
		return fmt.Errorf("snapd API %s: status %d: %s", path, env.StatusCode, env.Result)
	}
	return json.Unmarshal(env.Result, out)
}

// unusedSnap is a support snap nothing installed needs.
type unusedSnap struct {
	snap snapInfo
	kind string   // "base" or "content snap"
	via  []string // its only users, themselves unused; empty if it never had any
}

// findUnused returns the support snaps (bases, and content providers with
// no apps of their own) that no other installed snap uses, in the order
// they can be removed. A snap counts as used by another that has it as
// base, is connected to its content, or names it as default provider of a
// content plug (connected or not). Snaps used only by unused snaps are
// unused too.
func findUnused(snaps []snapInfo, conns connections) []unusedSnap {
	installed := map[string]snapInfo{}
	for _, s := range snaps {
		if s.Status == "" || s.Status == "active" || s.Status == "installed" {
			installed[s.Name] = s
		}
	}
	providers := map[string]bool{}
	for _, sl := range conns.Slots {
		if sl.Interface == "content" {
			providers[sl.Snap] = true
		}
	}
	kind := func(s snapInfo) string {
		switch {
		case s.Type == "base":
			return "base"
		case s.Type == "app" && len(s.Apps) == 0 && providers[s.Name]:
			return "content snap"
		}
		return ""
	}

	users := map[string]map[string]bool{} // snap -> snaps that use it
	use := func(user, used string) {
		if user == used || used == "" {
			return
		}
		if users[used] == nil {
			users[used] = map[string]bool{}
		}
		users[used][user] = true
	}
	for _, s := range installed {
		use(s.Name, s.Base)
	}
	for _, c := range conns.Established {
		if c.Interface == "content" {
			use(c.Plug.Snap, c.Slot.Snap)
		}
	}
	for _, p := range conns.Plugs {
		if p.Interface != "content" {
			continue
		}
		if dp, ok := p.Attrs["default-provider"].(string); ok {
			name, _, _ := strings.Cut(dp, ":") // "snap" or "snap:slot"
			use(p.Snap, name)
		}
	}

	var names []string
	for n := range installed {
		names = append(names, n)
	}
	sort.Strings(names)

	unused := map[string]bool{}
	var out []unusedSnap
	for changed := true; changed; {
		changed = false
		for _, n := range names {
			s := installed[n]
			k := kind(s)
			if k == "" || unused[n] {
				continue
			}
			var via []string
			live := false
			for u := range users[n] {
				if _, ok := installed[u]; !ok {
					continue // e.g. a plug of a snap being removed
				}
				if !unused[u] {
					live = true
					break
				}
				via = append(via, u)
			}
			if live {
				continue
			}
			sort.Strings(via)
			unused[n] = true
			out = append(out, unusedSnap{snap: s, kind: k, via: via})
			changed = true
		}
	}
	return out
}

var seedName = regexp.MustCompile(`^\s*-?\s*name:\s*"?([a-z0-9-]+)"?\s*$`)

// seeded reads the names of snaps that came with the system image.
func seeded() map[string]bool {
	out := map[string]bool{}
	f, err := os.Open(seedFile)
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if m := seedName.FindStringSubmatch(sc.Text()); m != nil {
			out[m[1]] = true
		}
	}
	return out
}

func unusedSupport(ctx context.Context, _ engine.Input) (engine.Outcome, error) {
	var snaps []snapInfo
	if err := snapd(ctx, "/v2/snaps", &snaps); err != nil {
		return engine.Outcome{}, err
	}
	var conns connections
	if err := snapd(ctx, "/v2/connections?select=all", &conns); err != nil {
		return engine.Outcome{}, err
	}
	seed := seeded()
	var out engine.Outcome
	for _, u := range findUnused(snaps, conns) {
		s := u.snap
		path := filepath.Join(snapsDir, s.Name+"_"+s.Revision+".snap")
		size := s.InstalledSize
		if fi, err := os.Stat(path); err == nil {
			if st, ok := fi.Sys().(*syscall.Stat_t); ok {
				size = st.Blocks * 512
			}
		}
		detail := s.Kind() + u.kindDetail()
		if seed[s.Name] {
			detail += ", came with the system"
		} else if !s.InstallDate.IsZero() {
			detail += ", current revision from " + s.InstallDate.Format("2006-01-02")
		}
		out.Items = append(out.Items, report.Item{
			ID: s.Name, Label: s.Name, Detail: detail, Path: path, Size: size,
			Confidence: report.ConfidenceReview,
			Action:     &report.Action{Command: "sudo snap remove " + s.Name, NeedsRoot: true},
		})
	}
	if len(out.Items) > 1 {
		out.Summary = "remove in the order shown: later ones are used only by earlier ones"
	}
	return out, nil
}

func (u unusedSnap) kindDetail() string {
	if len(u.via) == 0 {
		return ", nothing uses it"
	}
	return ", used only by " + strings.Join(u.via, ", ") + " (also unused)"
}

// Kind is the snap's type for display.
func (s snapInfo) Kind() string {
	if s.Type == "base" {
		return "base"
	}
	return "content snap"
}
