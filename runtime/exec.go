//go:build linux

// Package runtime/exec handles container process lifecycle.
//
// Re-execution Pattern
// ====================
// Linux namespaces must be created at fork/clone time, not after exec.
// The Go runtime starts multiple OS threads before main() runs, making it
// impossible to call unshare(2) safely after startup.
//
// The solution used by runc and other runtimes (and by us) is:
//
//  1. Parent (outer) process: sets up state, creates a child via os/exec
//     with SysProcAttr.Cloneflags = CLONE_NEW{PID,NS,UTS,IPC,NET}.
//     The child binary is /proc/self/exe (the runtime binary itself).
//
//  2. Child process: receives "init" as its first argument and knows it is
//     running inside the new namespaces. It finishes namespace setup
//     (pivot_root, /proc mount, hostname, cgroup membership) and then
//     exec(2)s the actual container process (e.g. /bin/sh).
//
// This "re-exec yourself" approach is elegant because we don't need a
// separate binary — the same runtime binary acts as both the parent manager
// and the container init, distinguished by the "init" flag.
package runtime

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
	"myruntime/config"
)

// ContainerConfig is passed from parent to child via an environment variable
// (encoded as JSON) so the child knows what to set up.
const initConfigEnvKey = "MYRUNTIME_INIT_CONFIG"

// InitConfig holds all the information the child "init" process needs.
type InitConfig struct {
	// ContainerID is used for cgroup membership and logging.
	ContainerID string `json:"id"`

	// BundleDir is the OCI bundle directory path (on the host).
	BundleDir string `json:"bundle"`

	// Spec is the parsed OCI config.
	Spec *config.Spec `json:"spec"`

	// RootFS is the absolute path to the container rootfs.
	// If OverlayFS is used this will be the merged/ directory.
	RootFS string `json:"rootfs"`

	// UseOverlay indicates whether OverlayFS was mounted for this container.
	UseOverlay bool `json:"useOverlay"`
}

// StartContainer forks a child that re-executes this binary with "init" as the
// first argument, running inside a new set of Linux namespaces.
//
// Returns the child *os.Process on success.
func StartContainer(cfg *InitConfig) (*os.Process, error) {
	// Encode the InitConfig as JSON and pass it via environment variable.
	// Using an env var is simpler and more portable than a pipe or file.
	cfgJSON, err := json.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("encoding init config: %w", err)
	}

	// /proc/self/exe is a symlink to the current executable.
	// Re-executing ourselves with "init" lets the child detect it is the
	// container init and switch to the ContainerInit() code path.
	exe, err := os.Readlink("/proc/self/exe")
	if err != nil {
		return nil, fmt.Errorf("resolving /proc/self/exe: %w", err)
	}

	// Determine namespace clone flags from the OCI spec.
	var nsFlags uintptr
	if cfg.Spec.Linux != nil {
		nsFlags = NamespaceFlags(cfg.Spec.Linux.Namespaces)
	}

	// Build the child command.
	// os/exec.Cmd.SysProcAttr.Cloneflags passes flags to the clone(2) syscall
	// when spawning the child, creating fresh namespaces.
	cmd := exec.Command(exe, "init")
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	cmd.Env = append(os.Environ(), fmt.Sprintf("%s=%s", initConfigEnvKey, cfgJSON))

	cmd.SysProcAttr = &syscall.SysProcAttr{
		// Cloneflags creates new namespaces for the child at clone/fork time.
		Cloneflags: nsFlags,

		// Pdeathsig sends SIGKILL to the child when the parent exits.
		// This prevents orphaned container processes if the runtime crashes.
		Pdeathsig: unix.SIGKILL,
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting container process: %w", err)
	}

	return cmd.Process, nil
}

// RunInitProcess is called from ContainerInit (in the child) to complete
// namespace setup and exec the container's actual process.
//
// This function DOES NOT return on success — it calls exec(2).
func RunInitProcess() error {
	// Read the init config from environment.
	cfgJSON := os.Getenv(initConfigEnvKey)
	if cfgJSON == "" {
		return fmt.Errorf("missing %s environment variable", initConfigEnvKey)
	}

	var cfg InitConfig
	if err := json.Unmarshal([]byte(cfgJSON), &cfg); err != nil {
		return fmt.Errorf("parsing init config: %w", err)
	}

	spec := cfg.Spec

	// --- Step 1: Make all mounts private ---
	// This stops any mounts we create from propagating to the host namespace.
	if err := MakeRootPrivate(); err != nil {
		return fmt.Errorf("making root private: %w", err)
	}

	// --- Step 2: Mount OverlayFS (if requested) ---
	// The parent already prepared the overlay directories; we just need to
	// mount them here inside our mount namespace.
	if cfg.UseOverlay {
		dirs := DefaultOverlayDirs(cfg.BundleDir)
		if err := MountOverlay(dirs); err != nil {
			return fmt.Errorf("mounting overlay: %w", err)
		}
	}

	// --- Step 3: Mount /proc inside the rootfs ---
	// Must be done BEFORE pivot_root so the path resolves correctly.
	procDest := filepath.Join(cfg.RootFS, "proc")
	if err := MountProc(procDest); err != nil {
		return fmt.Errorf("mounting proc: %w", err)
	}

	// --- Step 4: pivot_root to the container rootfs ---
	if err := PivotRoot(cfg.RootFS); err != nil {
		// Fallback to chroot if pivot_root fails.
		// pivot_root fails in some nested virtualisation or user-ns-only setups.
		fmt.Fprintf(os.Stderr, "[myruntime/init] pivot_root failed (%v), falling back to chroot\n", err)
		if err2 := FallbackChroot(cfg.RootFS); err2 != nil {
			return fmt.Errorf("both pivot_root and chroot failed: pivot_root=%v chroot=%v", err, err2)
		}
	}

	// After pivot_root/chroot we are now inside the container rootfs.
	// All paths from here are relative to the new root.

	// --- Step 5: Process spec mounts (e.g. /dev, /sys) ---
	// /proc was already mounted before pivot_root; skip it here.
	var extraMounts []config.Mount
	for _, m := range spec.Mounts {
		if m.Destination == "/proc" {
			continue // already mounted
		}
		extraMounts = append(extraMounts, m)
	}
	if err := MountSpecMounts(extraMounts); err != nil {
		return fmt.Errorf("mounting spec mounts: %w", err)
	}

	// --- Step 6: Set hostname ---
	if err := SetHostname(spec.Hostname); err != nil {
		return fmt.Errorf("setting hostname: %w", err)
	}

	// --- Step 7: Change to the process working directory ---
	cwd := "/"
	if spec.Process != nil && spec.Process.Cwd != "" {
		cwd = spec.Process.Cwd
	}
	if err := os.MkdirAll(cwd, 0755); err != nil {
		return fmt.Errorf("creating cwd %q: %w", cwd, err)
	}
	if err := unix.Chdir(cwd); err != nil {
		return fmt.Errorf("chdir to %q: %w", cwd, err)
	}

	// --- Step 8: exec the container process ---
	// syscall.Exec replaces the current process image entirely (no fork).
	// This is the final step: the container binary runs as PID 1 (in the new
	// PID namespace) in the isolated rootfs.
	args := spec.Process.Args
	env := spec.Process.Env

	// Locate the binary (absolute path or PATH lookup).
	binary, err := lookupBinary(args[0])
	if err != nil {
		return fmt.Errorf("looking up binary %q: %w", args[0], err)
	}

	// exec(2): replace this process image. Does not return on success.
	if err := syscall.Exec(binary, args, env); err != nil {
		return fmt.Errorf("exec %q: %w", binary, err)
	}

	// Never reached.
	return nil
}

// WaitForProcess waits for the given process and updates the container state.
func WaitForProcess(proc *os.Process, id string) error {
	state, err := proc.Wait()
	if err != nil {
		return fmt.Errorf("waiting for container process: %w", err)
	}

	s, err := LoadState(id)
	if err != nil {
		return err
	}

	s.Status = StatusStopped
	s.PID = 0
	_ = state // could store exit code here

	return SaveState(s)
}

// lookupBinary resolves a binary name to an absolute path.
// If the name is already absolute, it is returned as-is.
// Otherwise, it is looked up in PATH.
func lookupBinary(name string) (string, error) {
	if filepath.IsAbs(name) {
		return name, nil
	}
	path, err := exec.LookPath(name)
	if err != nil {
		return "", err
	}
	return path, nil
}

// CreateContainerBundle creates a minimal OCI bundle structure:
//   <bundleDir>/rootfs/    — the container root filesystem
//   <bundleDir>/config.json
//
// Returns the absolute bundle directory path.
func CreateContainerBundle(containerID, baseDir string, spec *config.Spec) (string, error) {
	bundleDir := filepath.Join(baseDir, containerID)

	if err := os.MkdirAll(bundleDir, 0755); err != nil {
		return "", fmt.Errorf("creating bundle dir: %w", err)
	}

	rootfsDir := filepath.Join(bundleDir, spec.Root.Path)
	if err := os.MkdirAll(rootfsDir, 0755); err != nil {
		return "", fmt.Errorf("creating rootfs dir: %w", err)
	}

	return bundleDir, nil
}

// ElapsedSince formats the duration since t as a human-readable string.
func ElapsedSince(t time.Time) string {
	d := time.Since(t)
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh", int(d.Hours()))
}
