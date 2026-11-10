// Package execx runs external tools for bundled checks.
package execx

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Output runs name with args under the C locale (so output can be parsed)
// and returns stdout. A failure includes the tool's stderr in the error.
func Output(ctx context.Context, name string, args ...string) ([]byte, error) {
	out, stderr, err := run(ctx, name, args...)
	if err != nil {
		return nil, describe(name, args, stderr, err)
	}
	return out, nil
}

func run(ctx context.Context, name string, args ...string) ([]byte, string, error) {
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	return out, stderr.String(), err
}

func describe(name string, args []string, stderr string, err error) error {
	what := name
	if len(args) > 0 {
		what += " " + args[0]
	}
	if msg := strings.TrimSpace(stderr); msg != "" {
		return fmt.Errorf("%s: %s", what, msg)
	}
	return fmt.Errorf("%s: %w", what, err)
}
