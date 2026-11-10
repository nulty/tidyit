// Package core holds the checks that work on any Linux desktop with no
// external tools: they only need the kernel, the filesystem and the
// freedesktop (XDG) specs.
package core

import "tidyit/internal/engine"

// Plugins returns the core plugins in display order.
func Plugins() []*engine.Plugin {
	return []*engine.Plugin{Disk(), Paths(), Trash(), Autostart()}
}
