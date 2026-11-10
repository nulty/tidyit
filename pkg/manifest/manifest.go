// Package manifest describes a plugin: who it is, what it needs, which checks
// it provides and the typed options each check accepts. Built-in plugins and
// external plugins use the same structure.
package manifest

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// ProtocolVersion is the tidy <-> plugin contract version.
const ProtocolVersion = 1

// Manifest is a plugin's declaration, loaded from plugin.toml or built in.
type Manifest struct {
	ID          string   `toml:"id" json:"id"`
	Name        string   `toml:"name" json:"name"`
	Version     string   `toml:"version" json:"version"`
	Protocol    int      `toml:"protocol" json:"protocol"`
	Description string   `toml:"description" json:"description,omitempty"`
	Requires    []string `toml:"requires" json:"requires,omitempty"` // binaries on PATH
	NeedsRoot   bool     `toml:"needs_root" json:"needs_root,omitempty"`
	// Lock names a group whose checks never run at the same time as each
	// other, e.g. "dpkg" for anything that reads the dpkg database.
	Lock   string      `toml:"lock" json:"lock,omitempty"`
	Checks []CheckSpec `toml:"checks" json:"checks"`
}

// CheckSpec is one check a plugin provides.
type CheckSpec struct {
	ID          string            `toml:"id" json:"id"`
	Title       string            `toml:"title" json:"title"`
	Description string            `toml:"description" json:"description,omitempty"`
	Hint        string            `toml:"hint" json:"hint,omitempty"`
	Options     map[string]Option `toml:"options" json:"options,omitempty"`
	// Template checks have no default instance; they only run through
	// presets or user-defined instances (e.g. paths.scan).
	Template bool `toml:"template" json:"template,omitempty"`
	// Timeout is the default time limit for this check, e.g. "60s", for
	// checks known to be slow. Config can still override it.
	Timeout string `toml:"timeout" json:"timeout,omitempty"`
	// DefaultOff checks only run when enabled in config or named with
	// --check, e.g. slow checks or ones that wrap another tool.
	DefaultOff bool `toml:"default_off" json:"default_off,omitempty"`
}

// Check returns the spec with the given id.
func (m *Manifest) Check(id string) (*CheckSpec, bool) {
	for i := range m.Checks {
		if m.Checks[i].ID == id {
			return &m.Checks[i], true
		}
	}
	return nil, false
}

// OptionType is the small set of types an option can have.
type OptionType string

const (
	TypeInteger OptionType = "integer"
	TypeNumber  OptionType = "number"
	TypeBoolean OptionType = "boolean"
	TypeString  OptionType = "string"
	TypeStrings OptionType = "strings" // list of strings
)

// Option declares one typed setting. It is a small subset of JSON Schema so
// it can be validated, set from the CLI and turned into a GUI form.
type Option struct {
	Type        OptionType `toml:"type" json:"type"`
	Default     any        `toml:"default" json:"default"`
	Min         *float64   `toml:"min" json:"min,omitempty"`
	Max         *float64   `toml:"max" json:"max,omitempty"`
	Enum        []string   `toml:"enum" json:"enum,omitempty"`
	Description string     `toml:"description" json:"description,omitempty"`
}

// Values are resolved, validated option values for one check instance.
type Values map[string]any

func (v Values) Int(name string) int          { n, _ := v[name].(int); return n }
func (v Values) Float(name string) float64    { f, _ := v[name].(float64); return f }
func (v Values) Bool(name string) bool        { b, _ := v[name].(bool); return b }
func (v Values) String(name string) string    { s, _ := v[name].(string); return s }
func (v Values) Strings(name string) []string { s, _ := v[name].([]string); return s }

// Resolve applies layers of overrides on top of the spec's defaults, in
// order, and returns validated values. Unknown keys are an error so that a
// typo in config is reported rather than silently ignored.
func (c *CheckSpec) Resolve(layers ...map[string]any) (Values, error) {
	out := Values{}
	for name, opt := range c.Options {
		v, err := opt.coerce(opt.Default)
		if err != nil {
			return nil, fmt.Errorf("option %q: bad default: %w", name, err)
		}
		out[name] = v
	}
	for _, layer := range layers {
		for _, name := range sortedKeys(layer) {
			opt, ok := c.Options[name]
			if !ok {
				return nil, fmt.Errorf("unknown option %q (valid: %s)", name, strings.Join(sortedKeys(c.Options), ", "))
			}
			v, err := opt.coerce(layer[name])
			if err != nil {
				return nil, fmt.Errorf("option %q: %w", name, err)
			}
			out[name] = v
		}
	}
	return out, nil
}

// coerce converts a decoded value (from TOML, JSON or Go) to the option's
// Go type and checks its limits.
func (o Option) coerce(raw any) (any, error) {
	switch o.Type {
	case TypeInteger:
		var n int
		switch x := raw.(type) {
		case nil:
		case int:
			n = x
		case int64:
			n = int(x)
		case float64:
			if x != math.Trunc(x) {
				return nil, fmt.Errorf("want a whole number, got %v", x)
			}
			n = int(x)
		default:
			return nil, fmt.Errorf("want an integer, got %T", raw)
		}
		return n, o.checkRange(float64(n))
	case TypeNumber:
		var f float64
		switch x := raw.(type) {
		case nil:
		case int:
			f = float64(x)
		case int64:
			f = float64(x)
		case float64:
			f = x
		default:
			return nil, fmt.Errorf("want a number, got %T", raw)
		}
		return f, o.checkRange(f)
	case TypeBoolean:
		switch x := raw.(type) {
		case nil:
			return false, nil
		case bool:
			return x, nil
		}
		return nil, fmt.Errorf("want true or false, got %T", raw)
	case TypeString:
		var s string
		switch x := raw.(type) {
		case nil:
		case string:
			s = x
		default:
			return nil, fmt.Errorf("want a string, got %T", raw)
		}
		if len(o.Enum) > 0 && !contains(o.Enum, s) {
			return nil, fmt.Errorf("want one of %s, got %q", strings.Join(o.Enum, ", "), s)
		}
		return s, nil
	case TypeStrings:
		switch x := raw.(type) {
		case nil:
			return []string{}, nil
		case []string:
			return append([]string{}, x...), nil
		case []any:
			out := make([]string, 0, len(x))
			for _, e := range x {
				s, ok := e.(string)
				if !ok {
					return nil, fmt.Errorf("want a list of strings, found %T", e)
				}
				out = append(out, s)
			}
			return out, nil
		}
		return nil, fmt.Errorf("want a list of strings, got %T", raw)
	}
	return nil, fmt.Errorf("unsupported option type %q", o.Type)
}

func (o Option) checkRange(f float64) error {
	if o.Min != nil && f < *o.Min {
		return fmt.Errorf("must be at least %v, got %v", *o.Min, f)
	}
	if o.Max != nil && f > *o.Max {
		return fmt.Errorf("must be at most %v, got %v", *o.Max, f)
	}
	return nil
}

// Ptr is a helper for declaring Min and Max in Go code.
func Ptr(f float64) *float64 { return &f }

func contains(list []string, s string) bool {
	for _, e := range list {
		if e == s {
			return true
		}
	}
	return false
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
