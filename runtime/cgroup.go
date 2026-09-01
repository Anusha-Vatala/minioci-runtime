//go:build linux

// Package runtime/cgroup configures cgroup v2 resource limits.
//
// cgroups v2 Background
// =====================
// Control Groups (cgroups) allow the kernel to limit, account for, and isolate
// resource usage (CPU, memory, PIDs, I/O, etc.) of process groups.
//
// In cgroups v2 (unified hierarchy), the entire cgroup tree lives under
// a single tmpfs at /sys/fs/cgroup.  Each cgroup is a directory; settings are
// controlled by writing to files inside those directories.
//
// For myruntime we create a cgroup at:
//   /sys/fs/cgroup/myruntime/<container-id>/
//
// We write:
//   cpu.max        — "quota period" e.g. "100000 100000" = 100ms quota per 100ms period
//   memory.max     — memory limit in bytes, or "max" for unlimited
//   pids.max       — maximum number of PIDs, or "max" for unlimited
//   cgroup.procs   — write the container PID here to add it to the cgroup
//
// REQUIRES ROOT: Writing to /sys/fs/cgroup requires CAP_SYS_ADMIN.
package runtime

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"myruntime/config"
)

const (
	// cgroupRoot is the cgroup v2 unified hierarchy mount point.
	cgroupRoot = "/sys/fs/cgroup"

	// cgroupNamespace is the sub-directory under cgroupRoot for all myruntime containers.
	cgroupNamespace = "myruntime"
)

// cgroupPath returns the absolute path to the cgroup directory for a container.
func cgroupPath(containerID string) string {
	return filepath.Join(cgroupRoot, cgroupNamespace, containerID)
}

// SetupCgroup creates the cgroup directory and applies resource limits from
// the OCI spec. It does NOT add any process to the cgroup yet.
//
// Call AddProcessToCgroup after the container process is started.
func SetupCgroup(containerID string, resources *config.LinuxResources) error {
	path := cgroupPath(containerID)

	// Create the cgroup directory. The kernel automatically creates the
	// cgroup when you mkdir under a cgroup v2 tree.
	if err := os.MkdirAll(path, 0755); err != nil {
		return fmt.Errorf("creating cgroup dir %q: %w", path, err)
	}

	if resources == nil {
		return nil // no limits to apply
	}

	// --- CPU limits ---
	// cpu.max format: "<quota> <period>"
	// Both values are in microseconds.
	// "max 100000" means no quota (unlimited CPU), period = 100ms.
	// "50000 100000" means the cgroup can use at most 50ms per 100ms.
	if resources.CPU != nil {
		quota := "max"
		period := uint64(100000) // default 100ms period

		if resources.CPU.Period != nil && *resources.CPU.Period > 0 {
			period = *resources.CPU.Period
		}
		if resources.CPU.Quota != nil && *resources.CPU.Quota > 0 {
			quota = fmt.Sprintf("%d", *resources.CPU.Quota)
		}

		cpuMax := fmt.Sprintf("%s %d", quota, period)
		if err := writeCgroupFile(path, "cpu.max", cpuMax); err != nil {
			return fmt.Errorf("setting cpu.max: %w", err)
		}
	}

	// --- Memory limits ---
	// memory.max: maximum amount of memory the cgroup can use (bytes).
	// Write "max" for unlimited.
	if resources.Memory != nil && resources.Memory.Limit != nil {
		val := "max"
		if *resources.Memory.Limit > 0 {
			val = fmt.Sprintf("%d", *resources.Memory.Limit)
		}
		if err := writeCgroupFile(path, "memory.max", val); err != nil {
			return fmt.Errorf("setting memory.max: %w", err)
		}
	}

	// --- PID limits ---
	// pids.max: maximum number of processes/threads in this cgroup.
	// Write "max" for unlimited.
	if resources.Pids != nil {
		val := "max"
		if resources.Pids.Limit > 0 {
			val = fmt.Sprintf("%d", resources.Pids.Limit)
		}
		if err := writeCgroupFile(path, "pids.max", val); err != nil {
			return fmt.Errorf("setting pids.max: %w", err)
		}
	}

	return nil
}

// AddProcessToCgroup moves process pid into the container's cgroup by writing
// its PID to the cgroup.procs file.
//
// This must be called from the parent process (not inside the container) after
// the container process has been started, because cgroup.procs expects a host PID.
func AddProcessToCgroup(containerID string, pid int) error {
	path := cgroupPath(containerID)
	if err := writeCgroupFile(path, "cgroup.procs", fmt.Sprintf("%d", pid)); err != nil {
		return fmt.Errorf("adding pid %d to cgroup %q: %w", pid, containerID, err)
	}
	return nil
}

// RemoveCgroup removes the cgroup directory for a container.
// All processes must have exited or been moved out of the cgroup first.
func RemoveCgroup(containerID string) error {
	path := cgroupPath(containerID)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing cgroup dir %q: %w", path, err)
	}
	return nil
}

// ReadCgroupFile reads a cgroup control file and returns its trimmed content.
// Useful for inspecting current limits or statistics.
func ReadCgroupFile(containerID, filename string) (string, error) {
	path := filepath.Join(cgroupPath(containerID), filename)
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("reading cgroup file %q: %w", path, err)
	}
	return strings.TrimSpace(string(data)), nil
}

// writeCgroupFile writes value to filename inside the cgroup directory.
func writeCgroupFile(cgroupDir, filename, value string) error {
	path := filepath.Join(cgroupDir, filename)
	if err := os.WriteFile(path, []byte(value), 0); err != nil {
		return fmt.Errorf("writing %q to %q: %w", value, path, err)
	}
	return nil
}

// IsCgroupV2 reports whether the system uses cgroup v2 (unified hierarchy).
// It checks for the cgroup2 filesystem type at /sys/fs/cgroup.
func IsCgroupV2() bool {
	// On cgroup v2 systems, /sys/fs/cgroup/cgroup.controllers exists.
	_, err := os.Stat(filepath.Join(cgroupRoot, "cgroup.controllers"))
	return err == nil
}
