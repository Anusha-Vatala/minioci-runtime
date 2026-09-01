//go:build linux

// cmd/kill.go implements the `myruntime kill` sub-command.
//
// Sends a POSIX signal to the container's init process (or all processes
// via the cgroup kill interface if available).
//
// Signal Handling
// ===============
// We send the signal to the container's host PID recorded in state.json.
// Because the container runs in a PID namespace, the host process ID is
// different from PID 1 visible inside the container.
//
// For a clean shutdown, send SIGTERM (default) and allow the process to
// clean up.  Use SIGKILL only as a last resort.
//
// REQUIRES ROOT: kill(2) on a process owned by root requires root or matching UID.
package cmd

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"syscall"

	"myruntime/runtime"
)

func runKill(args []string) error {
	if os.Getuid() != 0 {
		return fmt.Errorf("'kill' requires root privileges (run with sudo)")
	}

	fs := flag.NewFlagSet("kill", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}

	if fs.NArg() < 1 {
		return fmt.Errorf("usage: myruntime kill <container-id> [signal]")
	}
	containerID := fs.Arg(0)

	// Default signal is SIGTERM — polite shutdown request.
	sigName := "SIGTERM"
	if fs.NArg() >= 2 {
		sigName = strings.ToUpper(fs.Arg(1))
	}

	sig, err := parseSignal(sigName)
	if err != nil {
		return err
	}

	// Load state.
	state, err := runtime.LoadState(containerID)
	if err != nil {
		return err
	}

	if state.Status == runtime.StatusStopped {
		return fmt.Errorf("container %q is already stopped", containerID)
	}

	if state.PID <= 0 {
		return fmt.Errorf("container %q has no recorded PID", containerID)
	}

	// Send signal to the container process.
	proc, err := os.FindProcess(state.PID)
	if err != nil {
		return fmt.Errorf("finding process %d: %w", state.PID, err)
	}

	if err := proc.Signal(sig); err != nil {
		// Process may have already exited.
		if strings.Contains(err.Error(), "process already finished") ||
			strings.Contains(err.Error(), "no such process") {
			fmt.Printf("[kill] process %d no longer exists\n", state.PID)
		} else {
			return fmt.Errorf("sending %s to pid %d: %w", sigName, state.PID, err)
		}
	} else {
		fmt.Printf("[kill] sent %s to container %q (pid %d)\n", sigName, containerID, state.PID)
	}

	// If we sent SIGKILL, mark the container as stopped immediately since
	// SIGKILL cannot be caught or ignored.
	if sig == syscall.SIGKILL {
		state.Status = runtime.StatusStopped
		state.PID = 0
		_ = runtime.SaveState(state)
		fmt.Printf("[kill] container %q marked as stopped\n", containerID)
	}

	return nil
}

// parseSignal converts a signal name (e.g. "SIGTERM", "TERM", "15") to a
// syscall.Signal value.
func parseSignal(name string) (syscall.Signal, error) {
	// Allow "SIG" prefix or bare name.
	name = strings.TrimPrefix(name, "SIG")

	sigMap := map[string]syscall.Signal{
		"HUP":    syscall.SIGHUP,
		"INT":    syscall.SIGINT,
		"QUIT":   syscall.SIGQUIT,
		"ILL":    syscall.SIGILL,
		"ABRT":   syscall.SIGABRT,
		"FPE":    syscall.SIGFPE,
		"KILL":   syscall.SIGKILL,
		"SEGV":   syscall.SIGSEGV,
		"PIPE":   syscall.SIGPIPE,
		"ALRM":   syscall.SIGALRM,
		"TERM":   syscall.SIGTERM,
		"CHLD":   syscall.SIGCHLD,
		"CONT":   syscall.SIGCONT,
		"STOP":   syscall.SIGSTOP,
		"TSTP":   syscall.SIGTSTP,
		"TTIN":   syscall.SIGTTIN,
		"TTOU":   syscall.SIGTTOU,
		"USR1":   syscall.SIGUSR1,
		"USR2":   syscall.SIGUSR2,
		"WINCH":  syscall.SIGWINCH,
	}

	// Try by name.
	if sig, ok := sigMap[name]; ok {
		return sig, nil
	}

	// Try as numeric string.
	var num int
	if _, err := fmt.Sscanf(name, "%d", &num); err == nil {
		return syscall.Signal(num), nil
	}

	return 0, fmt.Errorf("unknown signal %q (use e.g. SIGTERM, SIGKILL, or signal number)", name)
}
