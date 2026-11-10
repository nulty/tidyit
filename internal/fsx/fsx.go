// Package fsx holds filesystem helpers shared by checks.
package fsx

import (
	"bufio"
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Home returns the user's home directory.
func Home() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return "/"
	}
	return h
}

// Expand turns "~/x", "$VAR/x" and "xdg:DOWNLOAD" into absolute paths.
// xdg: names are those in user-dirs.dirs (DESKTOP, DOWNLOAD, DOCUMENTS,
// MUSIC, PICTURES, VIDEOS, TEMPLATES, PUBLICSHARE).
func Expand(p string) string {
	if name, ok := strings.CutPrefix(p, "xdg:"); ok {
		return XDGUserDir(name)
	}
	if p == "~" {
		return Home()
	}
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		return filepath.Join(Home(), rest)
	}
	return os.ExpandEnv(p)
}

// Pretty shortens a path under home to ~/...
func Pretty(p string) string {
	h := Home()
	if p == h {
		return "~"
	}
	if rest, ok := strings.CutPrefix(p, h+"/"); ok {
		return "~/" + rest
	}
	return p
}

// XDGUserDir reads $XDG_CONFIG_HOME/user-dirs.dirs, falling back to the
// usual English folder name under home.
func XDGUserDir(name string) string {
	name = strings.ToUpper(name)
	cfg, err := os.UserConfigDir()
	if err == nil {
		if f, err := os.Open(filepath.Join(cfg, "user-dirs.dirs")); err == nil {
			defer f.Close()
			sc := bufio.NewScanner(f)
			for sc.Scan() {
				k, v, ok := strings.Cut(strings.TrimSpace(sc.Text()), "=")
				if !ok || k != "XDG_"+name+"_DIR" {
					continue
				}
				v = strings.Trim(v, `"`)
				v = strings.ReplaceAll(v, "$HOME", Home())
				return v
			}
		}
	}
	fallback := map[string]string{"DESKTOP": "Desktop", "DOWNLOAD": "Downloads", "DOCUMENTS": "Documents",
		"MUSIC": "Music", "PICTURES": "Pictures", "VIDEOS": "Videos", "TEMPLATES": "Templates", "PUBLICSHARE": "Public"}
	if dir, ok := fallback[name]; ok {
		return filepath.Join(Home(), dir)
	}
	return filepath.Join(Home(), strings.ToLower(name))
}

// Stat summarises a file or directory tree.
type Stat struct {
	Size   int64     // bytes on disk (allocated blocks), not apparent size
	Files  int       // regular files counted
	Newest time.Time // most recent mtime anywhere inside
	Owner  uint32    // uid of the top-level entry
}

// TreeStat walks root (without following symlinks or crossing into other
// filesystems) and sums sizes. Unreadable parts are skipped, not fatal.
func TreeStat(ctx context.Context, root string) (Stat, error) {
	var st Stat
	info, err := os.Lstat(root)
	if err != nil {
		return st, err
	}
	sys, _ := info.Sys().(*syscall.Stat_t)
	if sys != nil {
		st.Owner = sys.Uid
	}
	dev := uint64(0)
	if sys != nil {
		dev = uint64(sys.Dev)
	}
	n := 0
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if n++; n%512 == 0 && ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return nil
		}
		if s, ok := fi.Sys().(*syscall.Stat_t); ok {
			if d.IsDir() && p != root && dev != 0 && uint64(s.Dev) != dev {
				return fs.SkipDir
			}
			st.Size += s.Blocks * 512
		}
		if fi.Mode().IsRegular() {
			st.Files++
		}
		if fi.ModTime().After(st.Newest) {
			st.Newest = fi.ModTime()
		}
		return nil
	})
	if err != nil && ctx.Err() != nil {
		return st, ctx.Err()
	}
	return st, nil
}

// Human formats bytes as 1.2K, 340M, 7.7G (powers of 1024, like du -h).
func Human(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%dB", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	v := float64(n) / float64(div)
	if v >= 10 {
		return fmt.Sprintf("%.0f%c", v, "KMGTPE"[exp])
	}
	return fmt.Sprintf("%.1f%c", v, "KMGTPE"[exp])
}

// Age describes a duration in days, months or years, for item details.
func Age(t time.Time) string {
	d := time.Since(t)
	days := int(d.Hours() / 24)
	switch {
	case days < 1:
		return "today"
	case days < 60:
		return fmt.Sprintf("%d days ago", days)
	case days < 730:
		return fmt.Sprintf("%d months ago", days/30)
	}
	return fmt.Sprintf("%d years ago", days/365)
}

// ParseDecimalSize reads sizes in decimal units as docker and BleachBit
// print them, e.g. "1.34GB", "16.4kB", "512B". Unknown forms give 0.
func ParseDecimalSize(s string) int64 {
	s = strings.TrimSpace(s)
	units := []struct {
		suffix string
		mult   float64
	}{{"TB", 1e12}, {"GB", 1e9}, {"MB", 1e6}, {"kB", 1e3}, {"KB", 1e3}, {"B", 1}}
	for _, u := range units {
		if num, ok := strings.CutSuffix(s, u.suffix); ok {
			f, err := strconv.ParseFloat(num, 64)
			if err != nil || f < 0 {
				return 0
			}
			return int64(f * u.mult)
		}
	}
	return 0
}
