package network

import (
	"reflect"
	"testing"
	"time"
)

func TestSplitTerse(t *testing.T) {
	got := splitTerse(`My\:Net:abc:802-11-wireless::yes:0`)
	want := []string{"My:Net", "abc", "802-11-wireless", "", "yes", "0"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q", got)
	}
	if got := splitTerse(`back\\slash:x`); !reflect.DeepEqual(got, []string{`back\slash`, "x"}) {
		t.Fatalf("got %q", got)
	}
}

func TestStale(t *testing.T) {
	now := time.Unix(1794326786, 0)
	out := []byte(`PLUSNET-FJ25:u1:802-11-wireless:wlp59s0:yes:1794326786
lo:u2:loopback:lo:no:1794302648
br-b3f7b2d6edc0:u3:bridge:br-b3f7b2d6edc0:no:1794302657
ACC_WIFI:u4:802-11-wireless::yes:0
br-4e700ad6e1d4:u5:bridge::no:1767646216
TN_Guest:u6:802-11-wireless::yes:1758035461
MyOpenVPN:u7:vpn::no:0
virbr0:u8:bridge::no:1767646216
ACC_WIFI:u9:802-11-wireless:wlp59s0:yes:1794326786
`)
	items := stale(parseConnections(out), now.AddDate(0, 0, -365))
	var got []string
	for _, it := range items {
		got = append(got, it.ID+" "+string(it.Confidence)+" "+it.Detail)
	}
	want := []string{
		"u4 review Wi-Fi, autoconnect on but never connected (2 connections share this name; its command uses the uuid)",
		"u6 review Wi-Fi, last used 2025-09-16",
		"u5 safe bridge with no device, likely a deleted Docker network",
		"u8 review bridge with no device",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}
