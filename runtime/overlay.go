//go:build linux

// Package runtime/overlay sets up OverlayFS for container rootfs layering.
//
// OverlayFS Background
// ====================
// OverlayFS is a union filesystem built into the Linux kernel that combines
// two filesystem trees into one unified view:
//
//   lowerdir  – read-only base layer (e.g., the container image rootfs)
//   upperdir  – read-write layer where all changes go (copy-on-write)
//   workdir   – internal scratch space for OverlayFS bookkeeping (must be
//               on the same filesystem as upperdir, must be empty)
//   merged    – the union mount point visible to the container process
//
// When a process inside the container reads a file:
//   - If it's been modified, the upperdir version is returned.
//   - If not, the lowerdir version is returned transparently.
//
// When a process writes a file:
//   - The file is copied from lowerdir to upperdir (copy-on-write), then modified.
//   - The lowerdir is NEVER modified.
//
// Overlay mounts require the kernel "overlay" module (modprobe overlay).
// Check support with: grep overlay /proc/filesystems
//
// REQUIRES ROOT: mount(2) requires CAP_SYS_ADMIN.
package runtime

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// OverlayDirs holds the paths for an OverlayFS mount.
type OverlayDirs struct {
	// LowerDir is the read-only base layer (the original rootfs).
	LowerDir string

	// UpperDir is the read-write layer for container modifications.
	UpperDir string

	// WorkDir is the internal OverlayFS work directory.
	// Must be on the same filesystem as UpperDir and must be empty.
	WorkDir string

	// MergedDir is the union mount target visible to the container.
	MergedDir string
}

// DefaultOverlayDirs constructs standard OverlayFS directory paths inside
// the bundle directory:
//
//	<bundle>/overlay/lower    → lowerdir (caller copies/links the image here)
//	<bundle>/overlay/upper    → upperdir
//	<bundle>/overlay/work     → workdir
//	<bundle>/overlay/merged   → mount point
func DefaultOverlayDirs(bundleDir string) OverlayDirs {
	base := filepath.Join(bundleDir, "overlay")
	return OverlayDirs{
		LowerDir:  filepath.Join(base, "lower"),
		UpperDir:  filepath.Join(base, "upper"),
		WorkDir:   filepath.Join(base, "work"),
		MergedDir: filepath.Join(base, "merged"),
	}
}

// PrepareOverlayDirs creates the required directories for an overlay mount.
// The LowerDir must already be populated by the caller (with the image rootfs).
func PrepareOverlayDirs(dirs OverlayDirs) error {
	for _, d := range []string{dirs.UpperDir, dirs.WorkDir, dirs.MergedDir} {
		if err := os.MkdirAll(d, 0755); err != nil {
			return fmt.Errorf("creating overlay dir %q: %w", d, err)
		}
	}
	// LowerDir must already exist (the image).
	if _, err := os.Stat(dirs.LowerDir); err != nil {
		return fmt.Errorf("lowerdir %q does not exist: %w", dirs.LowerDir, err)
	}
	return nil
}

// MountOverlay mounts an OverlayFS using the given directories.
//
// The mount options string for overlayfs looks like:
//   lowerdir=<lower>,upperdir=<upper>,workdir=<work>
//
// After this call, dirs.MergedDir is the union of lower and upper layers.
func MountOverlay(dirs OverlayDirs) error {
	// Build the options string for the overlay filesystem driver.
	// Note: paths must not contain commas (we trust our generated paths here).
	opts := fmt.Sprintf(
		"lowerdir=%s,upperdir=%s,workdir=%s",
		dirs.LowerDir, dirs.UpperDir, dirs.WorkDir,
	)

	// Mount the overlay filesystem.
	// - source "overlay" is just a label (the kernel ignores it for overlayfs)
	// - fstype "overlay" selects the overlay filesystem driver
	err := unix.Mount("overlay", dirs.MergedDir, "overlay", 0, opts)
	if err != nil {
		return fmt.Errorf("mounting overlayfs at %q: %w\n  opts: %s", dirs.MergedDir, err, opts)
	}

	return nil
}

// UnmountOverlay unmounts the overlay merged directory.
// Must be called before deleting the container.
func UnmountOverlay(mergedDir string) error {
	err := unix.Unmount(mergedDir, unix.MNT_DETACH)
	if err != nil && err != unix.EINVAL {
		return fmt.Errorf("unmounting overlay %q: %w", mergedDir, err)
	}
	return nil
}

// IsOverlaySupported reports whether the running kernel has OverlayFS support
// by checking /proc/filesystems.
func IsOverlaySupported() bool {
	data, err := os.ReadFile("/proc/filesystems")
	if err != nil {
		return false
	}
	// /proc/filesystems contains lines like "nodev\toverlay"
	for _, line := range splitLines(string(data)) {
		if filepath.Base(line) == "overlay" || line == "overlay" || len(line) >= 7 && line[len(line)-7:] == "overlay" {
			return true
		}
	}
	// Simpler check: does "overlay" appear anywhere in the file?
	for i := 0; i+7 <= len(data); i++ {
		if string(data[i:i+7]) == "overlay" {
			return true
		}
	}
	return false
}

// splitLines splits a string into non-empty lines.
func splitLines(s string) []string {
	var lines []string
	start := 0
	for i, c := range s {
		if c == '\n' {
			line := s[start:i]
			if len(line) > 0 {
				lines = append(lines, line)
			}
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}
