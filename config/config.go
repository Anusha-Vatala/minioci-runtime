// Package config implements OCI runtime spec parsing.
//
// We define only the fields that myruntime actually uses rather than
// importing the full OCI spec library, so you can see exactly what each
// field means and how it maps to Linux kernel features.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// ------------------- Top-level OCI Spec -------------------

// Spec is the root OCI runtime configuration structure.
// It corresponds to the config.json file in an OCI bundle.
type Spec struct {
	// OCIVersion is the version of the OCI Runtime Specification.
	OCIVersion string `json:"ociVersion"`

	// Process describes the container process to run.
	Process *Process `json:"process,omitempty"`

	// Root describes the container's root filesystem.
	Root *Root `json:"root,omitempty"`

	// Hostname is the UTS hostname inside the container.
	Hostname string `json:"hostname,omitempty"`

	// Mounts specifies additional mount points.
	Mounts []Mount `json:"mounts,omitempty"`

	// Linux contains Linux-specific configuration.
	Linux *Linux `json:"linux,omitempty"`
}

// ------------------- Process -------------------

// Process describes the container process.
type Process struct {
	// Terminal indicates whether a pseudo-terminal should be allocated.
	Terminal bool `json:"terminal,omitempty"`

	// User specifies the UID/GID inside the container.
	User User `json:"user"`

	// Args is the executable and its arguments.
	Args []string `json:"args"`

	// Env is the list of environment variables in "KEY=VALUE" format.
	Env []string `json:"env,omitempty"`

	// Cwd is the working directory for the process inside the container.
	Cwd string `json:"cwd"`

	// Capabilities holds the Linux capability sets for the process.
	Capabilities *LinuxCapabilities `json:"capabilities,omitempty"`
}

// User identifies the UID and GID inside the container.
type User struct {
	// UID is the user ID.
	UID uint32 `json:"uid"`

	// GID is the group ID.
	GID uint32 `json:"gid"`
}

// LinuxCapabilities specifies capability sets.
// We carry these through but do not enforce dropping them in this version.
type LinuxCapabilities struct {
	Bounding    []string `json:"bounding,omitempty"`
	Effective   []string `json:"effective,omitempty"`
	Permitted   []string `json:"permitted,omitempty"`
	Ambient     []string `json:"ambient,omitempty"`
	Inheritable []string `json:"inheritable,omitempty"`
}

// ------------------- Root Filesystem -------------------

// Root describes the container's root filesystem.
type Root struct {
	// Path is the path to the root filesystem directory or a tar archive.
	// Relative paths are resolved relative to the bundle directory.
	Path string `json:"path"`

	// Readonly makes the rootfs read-only when true.
	Readonly bool `json:"readonly,omitempty"`
}

// ------------------- Mounts -------------------

// Mount specifies a filesystem mount inside the container.
type Mount struct {
	// Destination is the absolute path inside the container namespace.
	Destination string `json:"destination"`

	// Type is the filesystem type (e.g., "proc", "tmpfs", "bind").
	Type string `json:"type,omitempty"`

	// Source is the host path or device to mount.
	Source string `json:"source,omitempty"`

	// Options are mount options passed to the mount syscall (e.g., "rbind", "ro").
	Options []string `json:"options,omitempty"`
}

// ------------------- Linux-specific -------------------

// Linux contains Linux-specific runtime configuration.
type Linux struct {
	// Namespaces lists the namespaces to create or join.
	Namespaces []LinuxNamespace `json:"namespaces,omitempty"`

	// Resources specifies cgroup resource limits.
	Resources *LinuxResources `json:"resources,omitempty"`

	// UIDMappings maps container UIDs to host UIDs (for user namespaces).
	UIDMappings []LinuxIDMapping `json:"uidMappings,omitempty"`

	// GIDMappings maps container GIDs to host GIDs (for user namespaces).
	GIDMappings []LinuxIDMapping `json:"gidMappings,omitempty"`

	// MaskedPaths are paths that should be inaccessible inside the container.
	MaskedPaths []string `json:"maskedPaths,omitempty"`

	// ReadonlyPaths are paths that should be read-only inside the container.
	ReadonlyPaths []string `json:"readonlyPaths,omitempty"`
}

// LinuxNamespace specifies a Linux namespace to create or join.
type LinuxNamespace struct {
	// Type is the namespace type: "pid", "mount", "uts", "ipc", "network", "user".
	Type LinuxNamespaceType `json:"type"`

	// Path, if set, is the path to an existing namespace file to join.
	// If empty, a new namespace of this type is created.
	Path string `json:"path,omitempty"`
}

// LinuxNamespaceType enumerates supported namespace types.
type LinuxNamespaceType string

const (
	PIDNamespace     LinuxNamespaceType = "pid"
	MountNamespace   LinuxNamespaceType = "mount"
	UTSNamespace     LinuxNamespaceType = "uts"
	IPCNamespace     LinuxNamespaceType = "ipc"
	NetworkNamespace LinuxNamespaceType = "network"
	UserNamespace    LinuxNamespaceType = "user"
)

// LinuxResources defines cgroup v2 resource limits.
type LinuxResources struct {
	// Memory holds memory-related cgroup v2 settings.
	Memory *LinuxMemory `json:"memory,omitempty"`

	// CPU holds CPU-related cgroup v2 settings.
	CPU *LinuxCPU `json:"cpu,omitempty"`

	// Pids limits the number of processes in the container.
	Pids *LinuxPids `json:"pids,omitempty"`
}

// LinuxMemory holds memory resource limits.
type LinuxMemory struct {
	// Limit is the memory limit in bytes. Written to memory.max.
	// -1 means unlimited ("max").
	Limit *int64 `json:"limit,omitempty"`
}

// LinuxCPU holds CPU resource limits.
type LinuxCPU struct {
	// Quota is the CPU time quota in microseconds per Period.
	// Written together with Period to cpu.max as "quota period".
	// -1 means unlimited ("max").
	Quota *int64 `json:"quota,omitempty"`

	// Period is the CPU period in microseconds.
	Period *uint64 `json:"period,omitempty"`
}

// LinuxPids limits the total number of processes.
type LinuxPids struct {
	// Limit is the maximum number of PIDs. Written to pids.max.
	// -1 means unlimited ("max").
	Limit int64 `json:"limit"`
}

// LinuxIDMapping maps a container ID range to a host ID range.
type LinuxIDMapping struct {
	// ContainerID is the starting ID inside the container.
	ContainerID uint32 `json:"containerID"`

	// HostID is the starting ID on the host.
	HostID uint32 `json:"hostID"`

	// Size is the number of IDs to map.
	Size uint32 `json:"size"`
}

// ------------------- Loader -------------------

// LoadSpec reads and parses the OCI config.json from the given bundle directory.
// The bundle directory is the directory containing config.json and the rootfs.
func LoadSpec(bundleDir string) (*Spec, error) {
	configPath := filepath.Join(bundleDir, "config.json")

	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("reading config.json from %q: %w", configPath, err)
	}

	var spec Spec
	if err := json.Unmarshal(data, &spec); err != nil {
		return nil, fmt.Errorf("parsing config.json: %w", err)
	}

	if err := validateSpec(&spec); err != nil {
		return nil, fmt.Errorf("invalid config.json: %w", err)
	}

	return &spec, nil
}

// validateSpec performs basic sanity checks on the parsed spec.
func validateSpec(spec *Spec) error {
	if spec.OCIVersion == "" {
		return fmt.Errorf("ociVersion is required")
	}
	if spec.Process == nil {
		return fmt.Errorf("process is required")
	}
	if len(spec.Process.Args) == 0 {
		return fmt.Errorf("process.args must not be empty")
	}
	if spec.Root == nil {
		return fmt.Errorf("root is required")
	}
	if spec.Root.Path == "" {
		return fmt.Errorf("root.path is required")
	}
	return nil
}

// RootFSPath returns the absolute path to the container rootfs given the bundle directory.
// If root.path is relative, it is resolved relative to the bundle directory.
func (s *Spec) RootFSPath(bundleDir string) string {
	if filepath.IsAbs(s.Root.Path) {
		return s.Root.Path
	}
	return filepath.Join(bundleDir, s.Root.Path)
}
