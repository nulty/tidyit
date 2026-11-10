// Package version reports which Tidy It build is running.
package version

import "runtime/debug"

// Version overrides the version Go records at build time. Set it when
// building outside a git checkout, e.g. from a release tarball:
//
//	go build -ldflags "-X tidyit/internal/version.Version=v0.1.0" ./cmd/tidyit
var Version string

// String is the running build's version. A build from a tagged commit
// reports the tag (v0.1.0-beta.1); an untagged one a Go pseudo-version
// (v0.1.0-beta.1.0.20261110223000-abcdef123456); either gains "+dirty"
// when built with uncommitted changes. Without version control
// information it is "dev".
func String() string {
	if Version != "" {
		return Version
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return "dev"
}

// Generator names this build in reports, e.g. "tidyit v0.1.0-beta.1".
func Generator() string { return "tidyit " + String() }
