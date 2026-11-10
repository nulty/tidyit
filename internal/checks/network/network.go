// Package network is a bundled plugin for NetworkManager connections.
package network

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"tidy/internal/engine"
	"tidy/internal/execx"
	"tidy/pkg/manifest"
	"tidy/pkg/report"
)

// Plugin returns the NetworkManager plugin.
func Plugin() *engine.Plugin {
	return &engine.Plugin{
		Manifest: manifest.Manifest{
			ID: "networkmanager", Name: "NetworkManager", Version: "0.1.0", Protocol: manifest.ProtocolVersion,
			Requires: []string{"nmcli"},
			Checks: []manifest.CheckSpec{{
				ID:    "stale-connections",
				Title: "Stale network connections",
				Hint:  "manage in Advanced Network Configuration, or: nmcli connection delete uuid <uuid>",
				Options: map[string]manifest.Option{
					"unused_days": {Type: manifest.TypeInteger, Default: 365, Min: manifest.Ptr(1),
						Description: "Flag Wi-Fi networks last used longer ago than this"},
				},
			}},
		},
		Checks: map[string]engine.CheckFunc{"stale-connections": staleConnections},
	}
}

type conn struct {
	name, uuid, typ, device string
	autoconnect             bool
	lastUsed                int64 // unix seconds, 0 = never
}

// splitTerse splits one line of `nmcli -t` output, where fields are
// separated by ':' and a literal ':' or '\' is escaped with '\'.
func splitTerse(line string) []string {
	var fields []string
	var cur strings.Builder
	for i := 0; i < len(line); i++ {
		switch c := line[i]; {
		case c == '\\' && i+1 < len(line):
			i++
			cur.WriteByte(line[i])
		case c == ':':
			fields = append(fields, cur.String())
			cur.Reset()
		default:
			cur.WriteByte(c)
		}
	}
	return append(fields, cur.String())
}

const fields = "NAME,UUID,TYPE,DEVICE,AUTOCONNECT,TIMESTAMP"

func parseConnections(out []byte) []conn {
	var conns []conn
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		f := splitTerse(sc.Text())
		if len(f) != 6 {
			continue
		}
		ts, _ := strconv.ParseInt(f[5], 10, 64)
		conns = append(conns, conn{name: f[0], uuid: f[1], typ: f[2], device: f[3], autoconnect: f[4] == "yes", lastUsed: ts})
	}
	return conns
}

// stale applies laptop-report's rules: bridges with no device (usually
// left by deleted Docker networks), autoconnect connections that have
// never connected, and Wi-Fi not used since cutoff.
func stale(conns []conn, cutoff time.Time) []report.Item {
	names := map[string]int{}
	for _, c := range conns {
		names[c.name]++
	}
	var items []report.Item
	for _, c := range conns {
		it := report.Item{ID: c.uuid, Label: c.name, Confidence: report.ConfidenceReview,
			Action: &report.Action{Command: "nmcli connection delete uuid " + c.uuid}}
		switch {
		case c.typ == "loopback":
			continue
		case c.typ == "bridge" && c.device == "":
			it.Detail = "bridge with no device"
			if strings.HasPrefix(c.name, "br-") {
				it.Detail += ", likely a deleted Docker network"
				it.Confidence = report.ConfidenceSafe
			}
		case c.autoconnect && c.lastUsed == 0:
			it.Detail = typeName(c.typ) + ", autoconnect on but never connected"
		case c.typ == "802-11-wireless" && c.lastUsed != 0 && time.Unix(c.lastUsed, 0).Before(cutoff):
			it.Detail = "Wi-Fi, last used " + time.Unix(c.lastUsed, 0).Format("2006-01-02")
		default:
			continue
		}
		if names[c.name] > 1 {
			it.Detail += fmt.Sprintf(" (%d connections share this name; its command uses the uuid)", names[c.name])
		}
		items = append(items, it)
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].Label < items[j].Label })
	return items
}

func typeName(t string) string {
	switch t {
	case "802-11-wireless":
		return "Wi-Fi"
	case "802-3-ethernet":
		return "Ethernet"
	}
	return t
}

func staleConnections(ctx context.Context, in engine.Input) (engine.Outcome, error) {
	raw, err := execx.Output(ctx, "nmcli", "-t", "-f", fields, "connection", "show")
	if err != nil {
		return engine.Outcome{}, err
	}
	cutoff := time.Now().AddDate(0, 0, -in.Options.Int("unused_days"))
	out := engine.Outcome{Items: stale(parseConnections(raw), cutoff)}
	return out, nil
}
