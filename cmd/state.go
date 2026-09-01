// cmd/state.go implements the `myruntime state` sub-command.
//
// Prints the current container state as JSON to stdout, following the OCI
// Runtime Specification state query format.
package cmd

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"myruntime/runtime"
)

func runState(args []string) error {
	fs := flag.NewFlagSet("state", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}

	if fs.NArg() < 1 {
		return fmt.Errorf("usage: myruntime state <container-id>")
	}
	containerID := fs.Arg(0)

	state, err := runtime.LoadState(containerID)
	if err != nil {
		return err
	}

	// If the state says "running", verify the process is still alive.
	// A crashed runtime may leave state.json in "running" without a live process.
	if state.Status == runtime.StatusRunning && state.PID > 0 {
		if !processAlive(state.PID) {
			state.Status = runtime.StatusStopped
			state.PID = 0
			// Persist the correction.
			_ = runtime.SaveState(state)
		}
	}

	out, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding state: %w", err)
	}

	fmt.Println(string(out))
	return nil
}

// processAlive reports whether a process with the given PID is running.
// On Linux, we check for the existence of /proc/<pid>.
func processAlive(pid int) bool {
	_, err := os.Stat(fmt.Sprintf("/proc/%d", pid))
	return err == nil
}
