package manifest

import (
	"reflect"
	"strings"
	"testing"
)

func spec() *CheckSpec {
	return &CheckSpec{ID: "c", Options: map[string]Option{
		"days":  {Type: TypeInteger, Default: 30, Min: Ptr(0), Max: Ptr(3650)},
		"ratio": {Type: TypeNumber, Default: 0.5},
		"on":    {Type: TypeBoolean, Default: true},
		"mode":  {Type: TypeString, Default: "a", Enum: []string{"a", "b"}},
		"globs": {Type: TypeStrings, Default: []string{"x"}},
	}}
}

func TestResolveDefaults(t *testing.T) {
	v, err := spec().Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if v.Int("days") != 30 || v.Float("ratio") != 0.5 || !v.Bool("on") || v.String("mode") != "a" ||
		!reflect.DeepEqual(v.Strings("globs"), []string{"x"}) {
		t.Fatalf("defaults wrong: %#v", v)
	}
}

func TestResolveLayersInOrder(t *testing.T) {
	// TOML decodes integers as int64 and arrays as []any.
	v, err := spec().Resolve(
		map[string]any{"days": 60, "mode": "b"},
		map[string]any{"days": int64(90), "globs": []any{"p:*", "q"}, "ratio": int64(2)},
	)
	if err != nil {
		t.Fatal(err)
	}
	if v.Int("days") != 90 || v.String("mode") != "b" || v.Float("ratio") != 2 ||
		!reflect.DeepEqual(v.Strings("globs"), []string{"p:*", "q"}) {
		t.Fatalf("got %#v", v)
	}
}

func TestResolveErrors(t *testing.T) {
	cases := map[string]map[string]any{
		"unknown option":   {"dayz": 1},
		"must be at least": {"days": -1},
		"must be at most":  {"days": 99999},
		"want one of":      {"mode": "c"},
		"want an integer":  {"days": "ten"},
		"want a whole":     {"days": 1.5},
		"want true or":     {"on": "yes"},
		"list of strings":  {"globs": []any{1}},
		"want a list":      {"globs": "x"},
	}
	for want, layer := range cases {
		_, err := spec().Resolve(layer)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%v: got %v, want error containing %q", layer, err, want)
		}
	}
}
