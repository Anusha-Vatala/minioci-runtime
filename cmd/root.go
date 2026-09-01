// Package cmd implements the myruntime CLI.
//
// Sub-commands follow the OCI Runtime Specification lifecycle:
//   create  – prepare namespace, cgroup, rootfs; enter "created" state
//   start   – exec the user process; enter "running" state
//   state   – query current container state
//   kill    – send a signal to the container process
//   delete  – clean up all resources; remove state
//
// Each sub-command is in its own file for clarity.
package cmd

import (
	"flag"
	"fmt"
	"os"
)

// Execute parses global flags and dispatches to the appropriate sub-command.
func Execute() error {
	// Global flags.
	debug := flag.Bool("debug", false, "enable debug logging")
	flag.Usage = usage
	flag.Parse()

	if *debug {
		// Enable verbose logging in the internal logger.
		// (Importing internal here would create a cycle; we use an env var instead.)
		os.Setenv("MYRUNTIME_DEBUG", "1")
	}

	args := flag.Args()
	if len(args) == 0 {
		usage()
		return nil
	}

	sub := args[0]
	subArgs := args[1:]

	switch sub {
	case "create":
		return runCreate(subArgs)
	case "start":
		return runStart(subArgs)
	case "state":
		return runState(subArgs)
	case "kill":
		return runKill(subArgs)
	case "delete":
		return runDelete(subArgs)
	case "help", "--help", "-h":
		usage()
		return nil
	default:
		return fmt.Errorf("unknown command %q — run 'myruntime help' for usage", sub)
	}
}

// usage prints the help text.
func usage() {
	fmt.Fprintf(os.Stderr, `myruntime — lightweight OCI-inspired container runtime

USAGE:
  myruntime [--debug] <command> [options]

COMMANDS:
  create  <id> --bundle <dir>  Create a container from an OCI bundle
  start   <id>                 Start the container process
  state   <id>                 Show container state (JSON)
  kill    <id> [signal]        Send a signal to the container (default: SIGTERM)
  delete  <id>                 Delete a stopped container and all its resources

GLOBAL FLAGS:
  --debug    Enable verbose debug logging

EXAMPLES:
  # Create a container from a bundle directory:
  sudo myruntime create mybox --bundle /path/to/bundle

  # Start the container:
  sudo myruntime start mybox

  # Check state:
  sudo myruntime state mybox

  # Stop the container:
  sudo myruntime kill mybox SIGTERM

  # Clean up:
  sudo myruntime delete mybox

NOTES:
  • Most operations require root privileges (sudo).
  • Tested on Linux with cgroups v2 and the overlay kernel module.
  • Bundle directory must contain config.json and the rootfs.

`)
}
