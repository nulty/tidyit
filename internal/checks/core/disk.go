package core

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"tidyit/internal/engine"
	"tidyit/internal/fsx"
	"tidyit/pkg/manifest"
	"tidyit/pkg/report"
)

// Filesystems that hold user data. Everything else (tmpfs, squashfs for
// snaps, overlay, proc, cgroup...) is ignored.
var diskFSTypes = map[string]bool{
	"ext2": true, "ext3": true, "ext4": true, "btrfs": true, "xfs": true, "f2fs": true,
	"zfs": true, "bcachefs": true, "vfat": true, "exfat": true, "ntfs": true, "ntfs3": true, "fuseblk": true,
}

// Disk reports filesystems that are nearly full.
func Disk() *engine.Plugin {
	return &engine.Plugin{
		Manifest: manifest.Manifest{
			ID: "disk", Name: "Disk", Version: "0.1.0", Protocol: manifest.ProtocolVersion,
			Checks: []manifest.CheckSpec{{
				ID:    "usage",
				Title: "Disk usage",
				Hint:  "see what is using the space in the sections below, or run: du -h --max-depth=1 <mount> | sort -rh",
				Options: map[string]manifest.Option{
					"warn_percent": {Type: manifest.TypeInteger, Default: 85, Min: manifest.Ptr(1), Max: manifest.Ptr(100),
						Description: "Flag a filesystem when it is at least this full"},
					"ignore_mounts": {Type: manifest.TypeStrings, Default: []string{},
						Description: "Mount points to leave out, e.g. /boot/efi"},
				},
			}},
		},
		Checks: map[string]engine.CheckFunc{"usage": diskUsage},
	}
}

type mount struct{ dev, dir, fstype string }

func diskUsage(_ context.Context, in engine.Input) (engine.Outcome, error) {
	mounts, err := readMounts("/proc/self/mounts")
	if err != nil {
		return engine.Outcome{}, err
	}
	warn := in.Options.Int("warn_percent")
	ignore := in.Options.Strings("ignore_mounts")
	var out engine.Outcome
	for _, m := range mounts {
		if matchAny(ignore, m.dir) {
			continue
		}
		var s syscall.Statfs_t
		if err := syscall.Statfs(m.dir, &s); err != nil || s.Blocks == 0 {
			continue
		}
		bs := uint64(s.Bsize)
		used := (s.Blocks - s.Bfree) * bs
		avail := s.Bavail * bs
		// Same formula as df: reserved blocks count as neither used nor free.
		pct := int(float64(used) / float64(used+avail) * 100)
		line := fmt.Sprintf("%-12s %3d%% used  %6s free of %s", m.dir, pct, fsx.Human(int64(avail)), fsx.Human(int64(s.Blocks*bs)))
		out.Notes = append(out.Notes, line)
		if pct >= warn {
			out.Items = append(out.Items, report.Item{
				ID: m.dir, Label: m.dir, Path: m.dir, // no Size: nothing here is removable as such
				Detail:     fmt.Sprintf("%d%% full, %s free", pct, fsx.Human(int64(avail))),
				Confidence: report.ConfidenceManual,
			})
		}
	}
	if len(out.Items) > 0 {
		out.Summary = fmt.Sprintf("at or above %d%% full", warn)
	}
	return out, nil
}

// readMounts returns one entry per block device, preferring the shortest
// mount path (btrfs subvolumes mount the same device several times).
func readMounts(path string) ([]mount, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []mount
	seen := map[string]int{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 3 || !diskFSTypes[fields[2]] {
			continue
		}
		m := mount{dev: fields[0], dir: unescapeMount(fields[1]), fstype: fields[2]}
		if i, ok := seen[m.dev]; ok {
			if len(m.dir) < len(out[i].dir) {
				out[i] = m
			}
			continue
		}
		seen[m.dev] = len(out)
		out = append(out, m)
	}
	return out, sc.Err()
}

// unescapeMount decodes the octal escapes /proc/mounts uses (\040 = space).
func unescapeMount(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			if n, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(n))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// matchAny reports whether name matches any glob pattern (or equals it).
func matchAny(patterns []string, name string) bool {
	for _, p := range patterns {
		if p == name {
			return true
		}
		if ok, _ := filepath.Match(p, name); ok {
			return true
		}
	}
	return false
}
