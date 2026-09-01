//go:build linux

// Package runtime/mount manages filesystem mount namespace setup.
//
// Mount Namespace Background
// ==========================
// When we create a new mount namespace (CLONE_NEWNS), the child inherits a
// COPY of the parent's mount table.  But by default, mounts can still
// propagate between namespaces (shared propagation).
//
// To fully isolate the container:
//  1. Remount the root "/" as MS_PRIVATE|MS_REC so that no future mounts
//     propagate back to the host.
//  2. Set up OverlayFS (or bind-mount) the container rootfs.
//  3. Mount /proc, /dev, /sys, and any spec-defined mounts inside the rootfs.
//  4. pivot_root to the new rootfs (preferred over chroot because it moves
//     the root and makes the old root inaccessible).
//  5. Unmount the old root that pivot_root put at /.pivot_old.
//
// REQUIRES ROOT: mount(2) and pivot_root(2) require CAP_SYS_ADMIN.
package runtime

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
	"myruntime/config"
)

// MakeRootPrivate remounts the current root filesystem with MS_PRIVATE|MS_REC
// propagation. This ensures that no mounts we create inside this mount
// namespace will propagate back to the host namespace.
//
// This is one of the most important steps: without it, mounting /proc inside
// the container would affect the host's mount table too.
func MakeRootPrivate() error {
	// MS_PRIVATE: mounts do not propagate between namespaces.
	// MS_REC:     apply recursively to all sub-mounts.
	err := unix.Mount("", "/", "", unix.MS_PRIVATE|unix.MS_REC, "")
	if err != nil {
		return fmt.Errorf("remounting / as private: %w", err)
	}
	return nil
}

// MountProc mounts a fresh procfs at the given destination path.
// /proc is essential for many tools (ps, top, /proc/self/exe, etc.) and for
// the PID namespace to work correctly (the container's /proc/1 is the init).
//
// The destination directory is created if it doesn't exist.
func MountProc(dest string) error {
	if err := os.MkdirAll(dest, 0755); err != nil {
		return fmt.Errorf("creating %q: %w", dest, err)
	}
	// Mount a new procfs. Flags:
	//   MS_NOEXEC   – do not allow program execution from /proc
	//   MS_NOSUID   – ignore setuid bits on /proc executables
	//   MS_NODEV    – do not interpret device files
	err := unix.Mount("proc", dest, "proc",
		unix.MS_NOEXEC|unix.MS_NOSUID|unix.MS_NODEV, "")
	if err != nil {
		return fmt.Errorf("mounting proc at %q: %w", dest, err)
	}
	return nil
}

// MountSysfs mounts sysfs at the given destination path.
func MountSysfs(dest string) error {
	if err := os.MkdirAll(dest, 0755); err != nil {
		return fmt.Errorf("creating %q: %w", dest, err)
	}
	err := unix.Mount("sysfs", dest, "sysfs",
		unix.MS_NOEXEC|unix.MS_NOSUID|unix.MS_NODEV|unix.MS_RDONLY, "")
	if err != nil {
		return fmt.Errorf("mounting sysfs at %q: %w", dest, err)
	}
	return nil
}

// MountTmpfs mounts a tmpfs at the given destination with the provided options.
func MountTmpfs(dest, options string) error {
	if err := os.MkdirAll(dest, 0755); err != nil {
		return fmt.Errorf("creating %q: %w", dest, err)
	}
	err := unix.Mount("tmpfs", dest, "tmpfs", unix.MS_NOSUID|unix.MS_NODEV, options)
	if err != nil {
		return fmt.Errorf("mounting tmpfs at %q: %w", dest, err)
	}
	return nil
}

// MountSpecMounts processes the Mounts list from the OCI spec and mounts each
// entry inside the container rootfs.
//
// This is called from inside the container's mount namespace, after pivot_root.
func MountSpecMounts(mounts []config.Mount) error {
	for _, m := range mounts {
		dest := m.Destination
		if err := applyMount(m); err != nil {
			return fmt.Errorf("mounting %q at %q: %w", m.Source, dest, err)
		}
	}
	return nil
}

// applyMount applies a single OCI mount entry.
func applyMount(m config.Mount) error {
	dest := m.Destination

	// Create the destination if it does not exist.
	if err := os.MkdirAll(dest, 0755); err != nil {
		return fmt.Errorf("MkdirAll %q: %w", dest, err)
	}

	// Parse mount options into syscall flags and data string.
	flags, data := parseMountOptions(m.Options)

	fstype := m.Type
	source := m.Source

	// For "bind" mounts, the source is a host directory.
	// MS_BIND instructs the kernel to bind-mount rather than create a new FS.
	return unix.Mount(source, dest, fstype, flags, data)
}

// parseMountOptions converts a list of option strings (as in config.json) into
// mount(2) flags and a data string passed to the filesystem driver.
func parseMountOptions(options []string) (uintptr, string) {
	var (
		flags uintptr
		data  []string
	)

	knownFlags := map[string]uintptr{
		"ro":          unix.MS_RDONLY,
		"rw":          0,
		"bind":        unix.MS_BIND,
		"rbind":       unix.MS_BIND | unix.MS_REC,
		"nosuid":      unix.MS_NOSUID,
		"nodev":       unix.MS_NODEV,
		"noexec":      unix.MS_NOEXEC,
		"sync":        unix.MS_SYNCHRONOUS,
		"dirsync":     unix.MS_DIRSYNC,
		"remount":     unix.MS_REMOUNT,
		"mand":        unix.MS_MANDLOCK,
		"noatime":     unix.MS_NOATIME,
		"nodiratime":  unix.MS_NODIRATIME,
		"relatime":    unix.MS_RELATIME,
		"strictatime": unix.MS_STRICTATIME,
		"shared":      unix.MS_SHARED,
		"slave":       unix.MS_SLAVE,
		"private":     unix.MS_PRIVATE,
		"unbindable":  unix.MS_UNBINDABLE,
	}

	for _, opt := range options {
		if f, ok := knownFlags[strings.ToLower(opt)]; ok {
			flags |= f
		} else {
			// Unknown option → pass as filesystem-specific data string.
			// e.g. "mode=755", "size=65536k"
			data = append(data, opt)
		}
	}

	return flags, strings.Join(data, ",")
}

// PivotRoot changes the root filesystem of the current process to newRoot.
//
// pivot_root(new_root, put_old):
//   - Moves the current root mount to put_old (a directory inside new_root).
//   - Makes new_root the new root filesystem.
//
// After pivoting, we unmount the old root (put_old) so it becomes inaccessible.
// This is more secure than chroot because chroot can be escaped; pivot_root
// actually changes the process's root mount point in the kernel.
//
// REQUIRES ROOT and a mount namespace (CLONE_NEWNS must have been used).
func PivotRoot(newRoot string) error {
	// put_old is a temporary directory inside newRoot where the old root
	// will be temporarily mounted during the pivot.
	putOld := filepath.Join(newRoot, ".pivot_old")

	if err := os.MkdirAll(putOld, 0700); err != nil {
		return fmt.Errorf("creating pivot_old dir %q: %w", putOld, err)
	}

	// Bind-mount newRoot onto itself. pivot_root requires both new_root and
	// put_old to be mount points, and new_root cannot be the same filesystem
	// as the current root. The self-bind mount satisfies this requirement.
	if err := unix.Mount(newRoot, newRoot, "", unix.MS_BIND|unix.MS_REC, ""); err != nil {
		return fmt.Errorf("bind mounting newRoot %q: %w", newRoot, err)
	}

	// Perform the pivot_root syscall.
	if err := unix.PivotRoot(newRoot, putOld); err != nil {
		return fmt.Errorf("pivot_root(%q, %q): %w", newRoot, putOld, err)
	}

	// After pivot_root, our working directory may be on the old root.
	// Change to the new root.
	if err := unix.Chdir("/"); err != nil {
		return fmt.Errorf("chdir after pivot_root: %w", err)
	}

	// Unmount the old root (now at /.pivot_old) and remove the directory.
	// MNT_DETACH: performs a lazy unmount — detaches the filesystem from the
	// hierarchy but allows ongoing file accesses to complete.
	if err := unix.Unmount("/.pivot_old", unix.MNT_DETACH); err != nil {
		return fmt.Errorf("unmounting old root /.pivot_old: %w", err)
	}

	if err := os.Remove("/.pivot_old"); err != nil {
		return fmt.Errorf("removing /.pivot_old: %w", err)
	}

	return nil
}

// FallbackChroot changes root via chroot(2) when pivot_root is unavailable.
//
// chroot is simpler but less secure. It only changes the apparent root for
// filesystem path lookups; it does not change the root mount point, so a
// process with the right capabilities can break out of a chroot jail.
//
// We use this as a fallback when:
//   - The system does not have CLONE_NEWNS support, OR
//   - We are inside a user namespace without the required capabilities.
func FallbackChroot(newRoot string) error {
	if err := syscall.Chroot(newRoot); err != nil {
		return fmt.Errorf("chroot(%q): %w", newRoot, err)
	}
	if err := unix.Chdir("/"); err != nil {
		return fmt.Errorf("chdir after chroot: %w", err)
	}
	return nil
}

// SetupDevSymlinks creates standard /dev symlinks inside the container rootfs.
// Many tools expect these to exist; they are typically provided by udev on a
// real system but we create them manually since we don't run udev.
func SetupDevSymlinks(devDir string) error {
	symlinks := map[string]string{
		"fd":     "/proc/self/fd",
		"stdin":  "/proc/self/fd/0",
		"stdout": "/proc/self/fd/1",
		"stderr": "/proc/self/fd/2",
	}
	for name, target := range symlinks {
		path := filepath.Join(devDir, name)
		// Remove existing entry (if any) and create symlink.
		_ = os.Remove(path)
		if err := os.Symlink(target, path); err != nil {
			return fmt.Errorf("creating symlink %q -> %q: %w", path, target, err)
		}
	}
	return nil
}

// MountDevPts mounts devpts (pseudo-terminal device filesystem) for terminal support.
func MountDevPts(dest string) error {
	if err := os.MkdirAll(dest, 0755); err != nil {
		return fmt.Errorf("creating %q: %w", dest, err)
	}
	return unix.Mount("devpts", dest, "devpts",
		unix.MS_NOSUID|unix.MS_NOEXEC,
		"newinstance,ptmxmode=0666,mode=0620")
}
