//go:build linux

// cmd/create.go implements the `myruntime create` sub-command.
//
// The OCI create command:
//   1. Reads and validates config.json from the bundle directory.
//   2. Optionally sets up OverlayFS if the bundle has an overlay/ directory.
//   3. Creates the cgroup for the container.
//   4. Writes the initial state.json (status=created).
//
// It does NOT start the container process — that is done by `myruntime start`.
// This separation allows hooks (pre-start, post-start) to run between create
// and start, following the OCI lifecycle model.
//
// REQUIRES ROOT: cgroup creation and (optional) overlay mount need CAP_SYS_ADMIN.
package cmd

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"myruntime/config"
	"myruntime/runtime"
)

func runCreate(args []string) error {
	// --- Require root ---
	if os.Getuid() != 0 {
		return fmt.Errorf("'create' requires root privileges (run with sudo)")
	}

	// --- Parse flags ---
	fs := flag.NewFlagSet("create", flag.ContinueOnError)
	bundleFlag := fs.String("bundle", "", "path to OCI bundle directory (required)")
	overlayFlag := fs.Bool("overlay", false, "use OverlayFS for rootfs isolation")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if fs.NArg() < 1 {
		return fmt.Errorf("usage: myruntime create <container-id> --bundle <dir>")
	}
	containerID := fs.Arg(0)

	if *bundleFlag == "" {
		return fmt.Errorf("--bundle is required")
	}

	bundleDir, err := filepath.Abs(*bundleFlag)
	if err != nil {
		return fmt.Errorf("resolving bundle path: %w", err)
	}

	// --- Check for duplicate ---
	if runtime.ContainerExists(containerID) {
		return fmt.Errorf("container %q already exists", containerID)
	}

	// --- Load OCI spec ---
	fmt.Printf("[create] loading config.json from %s\n", bundleDir)
	spec, err := config.LoadSpec(bundleDir)
	if err != nil {
		return fmt.Errorf("loading spec: %w", err)
	}

	// --- Determine rootfs path ---
	rootfs := spec.RootFSPath(bundleDir)

	// --- OverlayFS setup ---
	useOverlay := *overlayFlag
	if useOverlay {
		if !runtime.IsOverlaySupported() {
			return fmt.Errorf("overlayfs not supported by this kernel (check: grep overlay /proc/filesystems)")
		}
		dirs := runtime.DefaultOverlayDirs(bundleDir)
		// If lowerdir doesn't exist yet, use the rootfs as the lower layer.
		if _, err := os.Stat(dirs.LowerDir); os.IsNotExist(err) {
			fmt.Printf("[create] using rootfs as overlayfs lowerdir: %s\n", rootfs)
			dirs.LowerDir = rootfs
		}
		if err := runtime.PrepareOverlayDirs(dirs); err != nil {
			return fmt.Errorf("preparing overlay dirs: %w", err)
		}
		// The merged dir becomes the effective rootfs for the container.
		rootfs = dirs.MergedDir
		fmt.Printf("[create] overlayfs prepared, merged dir: %s\n", rootfs)
	} else {
		// Verify rootfs exists.
		if _, err := os.Stat(rootfs); err != nil {
			return fmt.Errorf("rootfs %q not found: %w", rootfs, err)
		}
	}

	// --- cgroup v2 setup ---
	if !runtime.IsCgroupV2() {
		fmt.Fprintln(os.Stderr, "[create] WARNING: cgroup v2 not detected — resource limits may not apply")
	}

	var resources *config.LinuxResources
	if spec.Linux != nil {
		resources = spec.Linux.Resources
	}

	fmt.Printf("[create] setting up cgroup for container %q\n", containerID)
	if err := runtime.SetupCgroup(containerID, resources); err != nil {
		return fmt.Errorf("setting up cgroup: %w", err)
	}

	// --- Write initial state ---
	state := &runtime.State{
		Version:    spec.OCIVersion,
		ID:         containerID,
		Status:     runtime.StatusCreated,
		PID:        0,
		BundlePath: bundleDir,
		Created:    time.Now(),
		Annotations: map[string]string{
			"rootfs":     rootfs,
			"useOverlay": fmt.Sprintf("%v", useOverlay),
		},
	}

	if err := runtime.SaveState(state); err != nil {
		return fmt.Errorf("saving state: %w", err)
	}

	fmt.Printf("[create] container %q created successfully\n", containerID)
	fmt.Printf("         bundle:  %s\n", bundleDir)
	fmt.Printf("         rootfs:  %s\n", rootfs)
	fmt.Printf("         overlay: %v\n", useOverlay)
	fmt.Printf("  Run 'myruntime start %s' to start the container.\n", containerID)
	return nil
}
