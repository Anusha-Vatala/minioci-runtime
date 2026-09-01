//go:build linux

// cmd/delete.go implements the `myruntime delete` sub-command.
//
// Cleans up all resources associated with a stopped container:
//   1. Verifies the container is not running (or --force kills it first).
//   2. Unmounts the OverlayFS merged directory (if used).
//   3. Removes the cgroup.
//   4. Deletes the state directory from /run/myruntime/<id>/.
//
// REQUIRES ROOT: unmounting and cgroup removal need CAP_SYS_ADMIN.
package cmd

import (
	"flag"
	"fmt"
	"os"
	"syscall"

	"myruntime/runtime"
)

func runDelete(args []string) error {
	if os.Getuid() != 0 {
		return fmt.Errorf("'delete' requires root privileges (run with sudo)")
	}

	fs := flag.NewFlagSet("delete", flag.ContinueOnError)
	force := fs.Bool("force", false, "kill the container if it is still running before deleting")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if fs.NArg() < 1 {
		return fmt.Errorf("usage: myruntime delete <container-id> [--force]")
	}
	containerID := fs.Arg(0)

	// Load state.
	state, err := runtime.LoadState(containerID)
	if err != nil {
		return err
	}

	// Refuse to delete a running container unless --force is used.
	if state.Status == runtime.StatusRunning && state.PID > 0 {
		if !*force {
			return fmt.Errorf(
				"container %q is running (pid %d) — use --force to kill it first",
				containerID, state.PID,
			)
		}

		// Force-kill the process.
		fmt.Printf("[delete] force-killing container %q (pid %d)\n", containerID, state.PID)
		proc, err := os.FindProcess(state.PID)
		if err == nil {
			_ = proc.Signal(syscall.SIGKILL)
			_, _ = proc.Wait()
		}
	}

	// --- Unmount OverlayFS (if used) ---
	if state.Annotations != nil && state.Annotations["useOverlay"] == "true" {
		dirs := runtime.DefaultOverlayDirs(state.BundlePath)
		fmt.Printf("[delete] unmounting overlayfs merged dir %s\n", dirs.MergedDir)
		if err := runtime.UnmountOverlay(dirs.MergedDir); err != nil {
			// Log but don't abort — we still want to clean up the rest.
			fmt.Fprintf(os.Stderr, "[delete] WARNING: unmounting overlay: %v\n", err)
		}
	}

	// --- Remove cgroup ---
	fmt.Printf("[delete] removing cgroup for container %q\n", containerID)
	if err := runtime.RemoveCgroup(containerID); err != nil {
		fmt.Fprintf(os.Stderr, "[delete] WARNING: removing cgroup: %v\n", err)
	}

	// --- Remove state files ---
	fmt.Printf("[delete] removing state for container %q\n", containerID)
	if err := runtime.DeleteState(containerID); err != nil {
		return fmt.Errorf("deleting state: %w", err)
	}

	fmt.Printf("[delete] container %q deleted successfully\n", containerID)
	return nil
}
