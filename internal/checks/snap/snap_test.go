package snap

import (
	"reflect"
	"testing"
)

func TestParseDisabled(t *testing.T) {
	out := []byte(`Name      Version   Rev    Tracking       Publisher     Notes
bare      1.0       5      latest/stable  canonical**   base
core20    20260410  2866   latest/stable  canonical**   base,disabled
core20    20260901  2922   latest/stable  canonical**   base
firefox   140.0     6100   latest/stable  mozilla**     disabled
firefox   141.0     6200   latest/stable  mozilla**     -
`)
	got := parseDisabled(out)
	want := []revision{{"core20", "2866"}, {"firefox", "6100"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}
