package docker

import (
	"testing"

	"tidyit/internal/fsx"
)

func TestParseSize(t *testing.T) {
	cases := map[string]int64{"1.34GB": 1_340_000_000, "512kB": 512_000, "9.93kB": 9930, "0B": 0, "N/A": 0, "478MB": 478_000_000}
	for in, want := range cases {
		if got := fsx.ParseDecimalSize(in); got != want {
			t.Errorf("parseSize(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestLabel(t *testing.T) {
	l := "com.docker.compose.network=default,com.docker.compose.project=hedging-ledger"
	if got := label(l, "com.docker.compose.project"); got != "hedging-ledger" {
		t.Errorf("got %q", got)
	}
	if got := label("", "x"); got != "" {
		t.Errorf("got %q", got)
	}
}

func TestIgnored(t *testing.T) {
	if !ignored([]string{"postgres:*"}, []string{"postgres:16"}) {
		t.Error("postgres:16 should match postgres:*")
	}
	if ignored([]string{"postgres:*"}, []string{"mysql:5.7"}) {
		t.Error("mysql should not match")
	}
}
