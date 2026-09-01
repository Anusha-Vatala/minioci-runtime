// Package runtime/state manages the on-disk lifecycle state of containers.
//
// Each container gets a directory under /run/myruntime/<id>/ that holds:
//   - state.json : current status, PID, bundle path, timestamps
//
// This package is platform-independent (pure Go, no syscalls) and can be
// tested on Windows.
package runtime

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Status represents the OCI-defined container lifecycle status.
type Status string

const (
	// StatusCreating - resources are being set up.
	StatusCreating Status = "creating"

	// StatusCreated - the container is created but the user process has not
	// been started yet (i.e. config.json parsed, namespaces set up, waiting
	// for `start` command).
	StatusCreated Status = "created"

	// StatusRunning - the user-specified process is running.
	StatusRunning Status = "running"

	// StatusStopped - the process has exited.
	StatusStopped Status = "stopped"
)

// State is the container state stored in state.json.
// Fields follow the OCI state schema where possible.
type State struct {
	// Version is the OCI spec version (informational).
	Version string `json:"ociVersion"`

	// ID is the container identifier.
	ID string `json:"id"`

	// Status is the current lifecycle status.
	Status Status `json:"status"`

	// PID is the host PID of the container init process.
	// 0 when the container is stopped or not yet started.
	PID int `json:"pid"`

	// BundlePath is the absolute path to the OCI bundle directory.
	BundlePath string `json:"bundle"`

	// Created is when the container was first created.
	Created time.Time `json:"created"`

	// Annotations carries arbitrary key-value pairs (optional).
	Annotations map[string]string `json:"annotations,omitempty"`
}

// stateRootDir is the base directory for all container state files.
// Requires write access; /run is a tmpfs on most Linux systems.
const stateRootDir = "/run/myruntime"

// containerDir returns the state directory for a specific container.
func containerDir(id string) string {
	return filepath.Join(stateRootDir, id)
}

// statePath returns the path to state.json for a given container ID.
func statePath(id string) string {
	return filepath.Join(containerDir(id), "state.json")
}

// SaveState persists the container state to disk.
// Creates the container state directory if it does not exist.
func SaveState(s *State) error {
	dir := containerDir(s.ID)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("creating state dir %q: %w", dir, err)
	}

	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("marshalling state: %w", err)
	}

	path := statePath(s.ID)
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("writing state file %q: %w", path, err)
	}
	return nil
}

// LoadState reads state.json for the given container ID.
// Returns an error if the container does not exist.
func LoadState(id string) (*State, error) {
	path := statePath(id)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("container %q does not exist", id)
		}
		return nil, fmt.Errorf("reading state file %q: %w", path, err)
	}

	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parsing state file %q: %w", path, err)
	}
	return &s, nil
}

// DeleteState removes all on-disk state for the given container ID.
func DeleteState(id string) error {
	dir := containerDir(id)
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("removing state dir %q: %w", dir, err)
	}
	return nil
}

// ContainerExists reports whether state exists for the given container ID.
func ContainerExists(id string) bool {
	_, err := os.Stat(statePath(id))
	return err == nil
}

// ListContainers returns the IDs of all containers that have state on disk.
func ListContainers() ([]string, error) {
	entries, err := os.ReadDir(stateRootDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // no containers yet
		}
		return nil, fmt.Errorf("reading state root %q: %w", stateRootDir, err)
	}

	var ids []string
	for _, e := range entries {
		if e.IsDir() {
			ids = append(ids, e.Name())
		}
	}
	return ids, nil
}
