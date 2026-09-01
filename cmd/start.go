//go:build linux

// cmd/start.go implements the `myruntime start` sub-command.
//
// The OCI start command launches the container process inside the previously
// created namespaces and cgroup. It:
//   1. Loads the existing "created" state.
//   2. Re-reads config.json from the bundle.
//   3. Forks a child that re-executes this binary with "init" (which calls
//      ContainerInit → RunInitProcess to complete namespace setup).
//   4. Moves the child PID into the container's cgroup.
//   5. Updates state to "running".
//   6. Waits for the process to exit (foreground) or returns (detached mode).
//
// REQUIRES ROOT: namespace clone, cgroup.procs write, mount syscalls.
package cmd

import (
	"flag"
	"fmt"
	"os"

	"myruntime/config"
	"myruntime/runtime"
)

func runStart(args []string) error {
	// --- Require root ---
	if os.Getuid() != 0 {
		return fmt.Errorf("'start' requires root privileges (run with sudo)")
	}

	// --- Parse flags ---
	fs := flag.NewFlagSet("start", flag.ContinueOnError)
	detach := fs.Bool("detach", false, "run container in the background")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if fs.NArg() < 1 {
		return fmt.Errorf("usage: myruntime start <container-id>")
	}
	containerID := fs.Arg(0)

	// --- Load state ---
	state, err := runtime.LoadState(containerID)
	if err != nil {
		return fmt.Errorf("loading state: %w", err)
	}

	if state.Status != runtime.StatusCreated {
		return fmt.Errorf("container %q is in state %q (expected %q)",
			containerID, state.Status, runtime.StatusCreated)
	}

	// --- Reload spec ---
	spec, err := config.LoadSpec(state.BundlePath)
	if err != nil {
		return fmt.Errorf("loading spec from bundle %q: %w", state.BundlePath, err)
	}

	// Determine effective rootfs (may be overlay merged dir).
	rootfs := spec.RootFSPath(state.BundlePath)
	useOverlay := false
	if state.Annotations != nil {
		if r, ok := state.Annotations["rootfs"]; ok && r != "" {
			rootfs = r
		}
		if v, ok := state.Annotations["useOverlay"]; ok && v == "true" {
			useOverlay = true
		}
	}

	// --- Build init config for the child process ---
	initCfg := &runtime.InitConfig{
		ContainerID: containerID,
		BundleDir:   state.BundlePath,
		Spec:        spec,
		RootFS:      rootfs,
		UseOverlay:  useOverlay,
	}

	fmt.Printf("[start] launching container %q\n", containerID)
	fmt.Printf("        rootfs:  %s\n", rootfs)
	fmt.Printf("        process: %v\n", spec.Process.Args)

	// --- Fork-exec the child in new namespaces ---
	proc, err := runtime.StartContainer(initCfg)
	if err != nil {
		return fmt.Errorf("starting container: %w", err)
	}

	// --- Add child to cgroup ---
	if err := runtime.AddProcessToCgroup(containerID, proc.Pid); err != nil {
		// Non-fatal: log and continue — the process is already running.
		fmt.Fprintf(os.Stderr, "[start] WARNING: failed to add pid %d to cgroup: %v\n", proc.Pid, err)
	}

	// --- Update state to running ---
	state.Status = runtime.StatusRunning
	state.PID = proc.Pid
	if err := runtime.SaveState(state); err != nil {
		return fmt.Errorf("saving running state: %w", err)
	}

	fmt.Printf("[start] container %q running with PID %d\n", containerID, proc.Pid)

	if *detach {
		// Background mode: return immediately; the child runs independently.
		fmt.Printf("[start] detached — use 'myruntime state %s' to monitor\n", containerID)
		return nil
	}

	// Foreground mode: wait for the container process to exit.
	fmt.Printf("[start] waiting for container to exit (Ctrl-C to detach)...\n")
	if err := runtime.WaitForProcess(proc, containerID); err != nil {
		return fmt.Errorf("waiting for container: %w", err)
	}

	fmt.Printf("[start] container %q has exited\n", containerID)
	return nil
}
