//go:build linux

// cmd/init.go contains ContainerInit(), which is the entry point for the
// child process that runs INSIDE the new Linux namespaces.
//
// This function is called from main.go when the first argument is "init".
// It must be called before the Go runtime starts any goroutines that might
// conflict with namespace setup.
//
// Execution Flow
// ==============
//  main() detects "init" argument
//    └─ ContainerInit()
//         └─ RunInitProcess()
//              ├─ MakeRootPrivate()
//              ├─ MountOverlay()         (if overlay enabled)
//              ├─ MountProc(rootfs/proc)
//              ├─ PivotRoot(rootfs)     → chroot fallback
//              ├─ MountSpecMounts()
//              ├─ SetHostname()
//              ├─ Chdir(cwd)
//              └─ syscall.Exec(binary, args, env)  ← replaces process image
//
// Signal Handling
// ===============
// As PID 1 in the new PID namespace, the container init process must handle
// signals explicitly. Linux does NOT deliver default signal actions to PID 1
// — for example, SIGTERM won't kill PID 1 by default. We install explicit
// handlers so the container can be stopped cleanly.
package cmd

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"myruntime/runtime"
)

// ContainerInit is the entry point for the child process running inside
// the new namespaces. It must not return — it calls os.Exit when done.
func ContainerInit() {
	// Install signal handlers BEFORE doing any namespace work.
	// As PID 1 in the new PID namespace, we must handle signals ourselves.
	setupSignalHandlers()

	// Run the full init sequence: set up mounts, pivot_root, exec.
	if err := runtime.RunInitProcess(); err != nil {
		fmt.Fprintf(os.Stderr, "[myruntime/init] fatal: %v\n", err)
		os.Exit(1)
	}

	// RunInitProcess calls syscall.Exec which replaces this process image.
	// If we somehow reach here, something went wrong.
	fmt.Fprintln(os.Stderr, "[myruntime/init] unexpected return from RunInitProcess")
	os.Exit(1)
}

// setupSignalHandlers installs handlers for common termination signals.
// This is necessary because PID 1 in a PID namespace receives signals
// differently from other processes (kernel doesn't auto-deliver SIGKILL
// to PID 1 for user-space initiated signals).
func setupSignalHandlers() {
	sigCh := make(chan os.Signal, 8)
	signal.Notify(sigCh,
		syscall.SIGTERM, // graceful shutdown
		syscall.SIGINT,  // Ctrl-C
		syscall.SIGHUP,  // terminal hangup
		syscall.SIGCHLD, // child process state change (reaping)
	)

	go func() {
		for sig := range sigCh {
			switch sig {
			case syscall.SIGCHLD:
				// Reap zombie child processes.
				// As PID 1, we are responsible for calling wait() on any
				// child processes that exit, otherwise they become zombies
				// and waste kernel resources.
				reapChildren()

			case syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP:
				// Forward the signal to all processes in our process group.
				// This ensures child processes of the container command also
				// receive the shutdown signal.
				_ = syscall.Kill(-1, sig.(syscall.Signal))
			}
		}
	}()
}

// reapChildren calls waitpid with WNOHANG to collect all exited children.
// This must be done by PID 1 to prevent zombie accumulation.
func reapChildren() {
	for {
		var status syscall.WaitStatus
		// WNOHANG: return immediately if no child has exited.
		// -1:      wait for any child process.
		pid, err := syscall.Wait4(-1, &status, syscall.WNOHANG, nil)
		if pid <= 0 || err != nil {
			// No more children to reap (or error).
			return
		}
		// pid > 0: a child exited; loop to check for more.
	}
}
