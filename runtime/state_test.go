// state_test.go - Unit tests for container state data structures.
//
// These tests write state to temp directories and are fully portable —
// they run on Windows and Linux without root.
//
// Run with: go test ./runtime/ -run TestState
package runtime

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeState marshals s and saves it under root/<id>/state.json.
func writeState(t *testing.T, root string, s *State) {
	t.Helper()
	dir := filepath.Join(root, s.ID)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "state.json"), data, 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

// readState reads and parses state.json from root/<id>/state.json.
func readState(t *testing.T, root, id string) *State {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, id, "state.json"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return &s
}

func TestState_Roundtrip(t *testing.T) {
	root := t.TempDir()

	original := &State{
		Version:    "1.0.2",
		ID:         "container-abc",
		Status:     StatusCreated,
		PID:        99,
		BundlePath: "/tmp/mybundle",
		Created:    time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC),
		Annotations: map[string]string{
			"author": "test",
		},
	}

	writeState(t, root, original)
	got := readState(t, root, original.ID)

	if got.ID != original.ID {
		t.Errorf("ID: got %q want %q", got.ID, original.ID)
	}
	if got.Status != StatusCreated {
		t.Errorf("Status: got %q want %q", got.Status, StatusCreated)
	}
	if got.PID != original.PID {
		t.Errorf("PID: got %d want %d", got.PID, original.PID)
	}
	if got.BundlePath != original.BundlePath {
		t.Errorf("BundlePath: got %q want %q", got.BundlePath, original.BundlePath)
	}
	if got.Annotations["author"] != "test" {
		t.Errorf("Annotation author: got %q want %q", got.Annotations["author"], "test")
	}
}

func TestStatus_Values(t *testing.T) {
	cases := []struct {
		status Status
		want   string
	}{
		{StatusCreating, "creating"},
		{StatusCreated, "created"},
		{StatusRunning, "running"},
		{StatusStopped, "stopped"},
	}
	for _, c := range cases {
		if string(c.status) != c.want {
			t.Errorf("Status %q: got string %q, want %q", c.status, string(c.status), c.want)
		}
	}
}

func TestState_StatusTransition(t *testing.T) {
	root := t.TempDir()

	s := &State{
		Version:    "1.0.2",
		ID:         "ct-transition",
		Status:     StatusCreating,
		BundlePath: "/tmp/b",
		Created:    time.Now(),
	}

	writeState(t, root, s)

	// Simulate status transitions.
	transitions := []Status{StatusCreated, StatusRunning, StatusStopped}
	for _, next := range transitions {
		s.Status = next
		writeState(t, root, s)
		got := readState(t, root, s.ID)
		if got.Status != next {
			t.Errorf("after transition to %q: got %q", next, got.Status)
		}
	}
}
