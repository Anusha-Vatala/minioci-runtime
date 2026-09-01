// main.go - Entry point for the myruntime container runtime.
//
// myruntime is a lightweight OCI-inspired container runtime that demonstrates
// how containers work under the hood using Linux namespaces, cgroups v2,
// pivot_root, and overlayfs — without relying on Docker, runc, or any
// existing container runtime library.
//
// REQUIRES ROOT: Most operations (namespace creation, cgroup writes, mount
// syscalls, pivot_root) require the process to run as root (UID 0).
package main

import (
	"fmt"
	"os"

	"myruntime/cmd"
)

func main() {
	// The runtime uses a special internal "init" sub-command that is invoked
	// when the process re-executes itself inside a new namespace set.
	// This must be checked BEFORE any flag parsing so the child process can
	// set up its environment before the parent continues.
	if len(os.Args) > 1 && os.Args[1] == "init" {
		// Hand off to the container init path immediately.
		// This code path runs INSIDE the new namespaces.
		cmd.ContainerInit()
		// ContainerInit calls os.Exit, so we never reach here.
	}

	// Normal CLI path — parse and execute user commands.
	if err := cmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
