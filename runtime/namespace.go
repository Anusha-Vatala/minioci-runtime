//go:build linux

// Package runtime/namespace sets up Linux namespaces for container isolation.
//
// Linux Namespaces Background
// ===========================
// A namespace wraps a global Linux resource so that processes inside the
// namespace see an isolated view of that resource. The kernel supports:
//
//   - PID namespace   – isolated process ID tree; the container init is PID 1
//   - Mount namespace – isolated filesystem mount table
//   - UTS namespace   – isolated hostname and domainname
//   - IPC namespace   – isolated System V IPC objects and POSIX message queues
//   - Network ns      – isolated network interfaces, routing, iptables rules
//   - User namespace  – isolated UID/GID ranges (allows unprivileged containers)
//
// We create all namespaces via the CLONE_NEW* flags passed to SysProcAttr
// when fork-exec-ing the child process.  The child process then writes UID/GID
// maps (for user namespaces) before exec-ing the final container binary.
package runtime

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
	"myruntime/config"
)

// NamespaceFlags converts OCI namespace specs to Linux clone flags.
// These flags are OR-ed together and passed to os/exec via SysProcAttr.Cloneflags.
//
// Each CLONE_NEW* constant instructs the kernel to create a fresh namespace
// of the given type for the new process, rather than inheriting the parent's.
func NamespaceFlags(namespaces []config.LinuxNamespace) uintptr {
	var flags uintptr

	for _, ns := range namespaces {
		switch ns.Type {
		case config.PIDNamespace:
			// CLONE_NEWPID: the new process becomes PID 1 of a fresh PID tree.
			// Child processes see PIDs starting from 1; the host still sees the
			// real PID.
			flags |= unix.CLONE_NEWPID

		case config.MountNamespace:
			// CLONE_NEWNS: the new process gets its own copy of the mount table.
			// Mounts made inside do not propagate to the host (after we set
			// MS_PRIVATE propagation).
			flags |= unix.CLONE_NEWNS

		case config.UTSNamespace:
			// CLONE_NEWUTS: the new process can set its own hostname and
			// NIS domainname without affecting the host.
			flags |= unix.CLONE_NEWUTS

		case config.IPCNamespace:
			// CLONE_NEWIPC: isolates System V IPC (semaphores, shared memory,
			// message queues) and POSIX message queues.
			flags |= unix.CLONE_NEWIPC

		case config.NetworkNamespace:
			// CLONE_NEWNET: gives the container its own network stack —
			// interfaces, routing tables, firewall rules, sockets.
			// By default only the loopback interface exists inside.
			flags |= unix.CLONE_NEWNET

		case config.UserNamespace:
			// CLONE_NEWUSER: maps host UID/GID ranges to container UID/GID ranges.
			// This allows a container to appear to run as root (UID 0) inside
			// while being a non-root user on the host. Requires UID/GID mappings.
			flags |= unix.CLONE_NEWUSER
		}
	}

	return flags
}

// SetHostname sets the UTS hostname inside the current UTS namespace.
// Must be called after unsharing the UTS namespace (i.e., inside the child).
//
// REQUIRES ROOT or a UTS namespace owned by the caller.
func SetHostname(hostname string) error {
	if hostname == "" {
		return nil
	}
	if err := unix.Sethostname([]byte(hostname)); err != nil {
		return fmt.Errorf("setting hostname %q: %w", hostname, err)
	}
	return nil
}

// WriteUserMappings writes UID and GID mappings for a user namespace.
//
// User namespace UID/GID mappings allow the kernel to translate IDs:
//   - "0 1000 1" means container UID 0 maps to host UID 1000, for 1 ID.
//
// We must write to /proc/<pid>/uid_map and /proc/<pid>/gid_map.
// Before writing gid_map we must also write "deny" to setgroups to prevent
// privilege escalation via setgroups(2) in some kernel versions.
//
// REQUIRES: the process identified by pid has already entered the user ns.
func WriteUserMappings(pid int, uidMaps, gidMaps []config.LinuxIDMapping) error {
	if len(uidMaps) == 0 && len(gidMaps) == 0 {
		return nil
	}

	// Write UID mappings.
	if len(uidMaps) > 0 {
		content := formatIDMappings(uidMaps)
		path := fmt.Sprintf("/proc/%d/uid_map", pid)
		if err := os.WriteFile(path, []byte(content), 0); err != nil {
			return fmt.Errorf("writing uid_map for pid %d: %w", pid, err)
		}
	}

	// Deny setgroups before writing gid_map (required on Linux >= 3.19).
	// This prevents a process from calling setgroups to drop supplementary
	// groups and gain access to files the original groups couldn't read.
	setgroupsPath := fmt.Sprintf("/proc/%d/setgroups", pid)
	if err := os.WriteFile(setgroupsPath, []byte("deny"), 0); err != nil {
		return fmt.Errorf("writing setgroups for pid %d: %w", pid, err)
	}

	// Write GID mappings.
	if len(gidMaps) > 0 {
		content := formatIDMappings(gidMaps)
		path := fmt.Sprintf("/proc/%d/gid_map", pid)
		if err := os.WriteFile(path, []byte(content), 0); err != nil {
			return fmt.Errorf("writing gid_map for pid %d: %w", pid, err)
		}
	}

	return nil
}

// formatIDMappings converts ID mapping structs to the kernel's text format:
//   containerID hostID size\n
func formatIDMappings(maps []config.LinuxIDMapping) string {
	var out string
	for _, m := range maps {
		out += fmt.Sprintf("%d %d %d\n", m.ContainerID, m.HostID, m.Size)
	}
	return out
}
