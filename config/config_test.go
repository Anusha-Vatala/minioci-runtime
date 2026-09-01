// config_test.go - Unit tests for the OCI spec parser.
//
// These tests are platform-independent and can be run on Windows or Linux
// with: go test ./config/...
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// helper: write a config.json to a temp bundle dir and return the dir path.
func writeTempConfig(t *testing.T, spec Spec) string {
	t.Helper()
	dir := t.TempDir()
	data, err := json.MarshalIndent(spec, "", "  ")
	if err != nil {
		t.Fatalf("marshal spec: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), data, 0644); err != nil {
		t.Fatalf("write config.json: %v", err)
	}
	return dir
}

func TestLoadSpec_ValidMinimal(t *testing.T) {
	spec := Spec{
		OCIVersion: "1.0.2",
		Process: &Process{
			Args: []string{"/bin/sh"},
			Cwd:  "/",
		},
		Root: &Root{Path: "rootfs"},
	}
	dir := writeTempConfig(t, spec)

	got, err := LoadSpec(dir)
	if err != nil {
		t.Fatalf("LoadSpec error: %v", err)
	}
	if got.OCIVersion != "1.0.2" {
		t.Errorf("ociVersion: got %q, want %q", got.OCIVersion, "1.0.2")
	}
	if got.Process.Args[0] != "/bin/sh" {
		t.Errorf("args[0]: got %q, want /bin/sh", got.Process.Args[0])
	}
}

func TestLoadSpec_MissingFile(t *testing.T) {
	_, err := LoadSpec("/nonexistent/bundle/dir")
	if err == nil {
		t.Fatal("expected error for missing config.json, got nil")
	}
}

func TestLoadSpec_MissingOCIVersion(t *testing.T) {
	spec := Spec{
		// OCIVersion intentionally omitted
		Process: &Process{Args: []string{"/bin/sh"}, Cwd: "/"},
		Root:    &Root{Path: "rootfs"},
	}
	dir := writeTempConfig(t, spec)
	_, err := LoadSpec(dir)
	if err == nil {
		t.Fatal("expected validation error for missing ociVersion")
	}
}

func TestLoadSpec_MissingProcess(t *testing.T) {
	spec := Spec{
		OCIVersion: "1.0.2",
		Root:       &Root{Path: "rootfs"},
	}
	dir := writeTempConfig(t, spec)
	_, err := LoadSpec(dir)
	if err == nil {
		t.Fatal("expected validation error for missing process")
	}
}

func TestLoadSpec_EmptyArgs(t *testing.T) {
	spec := Spec{
		OCIVersion: "1.0.2",
		Process:    &Process{Args: []string{}, Cwd: "/"},
		Root:       &Root{Path: "rootfs"},
	}
	dir := writeTempConfig(t, spec)
	_, err := LoadSpec(dir)
	if err == nil {
		t.Fatal("expected validation error for empty args")
	}
}

func TestLoadSpec_MissingRoot(t *testing.T) {
	spec := Spec{
		OCIVersion: "1.0.2",
		Process:    &Process{Args: []string{"/bin/sh"}, Cwd: "/"},
	}
	dir := writeTempConfig(t, spec)
	_, err := LoadSpec(dir)
	if err == nil {
		t.Fatal("expected validation error for missing root")
	}
}

func TestLoadSpec_FullSpec(t *testing.T) {
	limit := int64(134217728) // 128 MiB
	quota := int64(100000)
	period := uint64(100000)
	pidsLimit := int64(64)

	spec := Spec{
		OCIVersion: "1.0.2",
		Hostname:   "mycontainer",
		Process: &Process{
			Args: []string{"/bin/sh", "-c", "echo hello"},
			Env:  []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"},
			Cwd:  "/",
			User: User{UID: 0, GID: 0},
		},
		Root: &Root{Path: "rootfs", Readonly: true},
		Mounts: []Mount{
			{Destination: "/proc", Type: "proc", Source: "proc"},
			{Destination: "/dev", Type: "tmpfs", Source: "tmpfs",
				Options: []string{"nosuid", "strictatime", "mode=755", "size=65536k"}},
		},
		Linux: &Linux{
			Namespaces: []LinuxNamespace{
				{Type: PIDNamespace},
				{Type: MountNamespace},
				{Type: UTSNamespace},
				{Type: IPCNamespace},
				{Type: NetworkNamespace},
			},
			Resources: &LinuxResources{
				Memory: &LinuxMemory{Limit: &limit},
				CPU:    &LinuxCPU{Quota: &quota, Period: &period},
				Pids:   &LinuxPids{Limit: pidsLimit},
			},
		},
	}

	dir := writeTempConfig(t, spec)
	got, err := LoadSpec(dir)
	if err != nil {
		t.Fatalf("LoadSpec error: %v", err)
	}

	// Check hostname
	if got.Hostname != "mycontainer" {
		t.Errorf("hostname: got %q, want %q", got.Hostname, "mycontainer")
	}

	// Check mounts
	if len(got.Mounts) != 2 {
		t.Errorf("mounts: got %d, want 2", len(got.Mounts))
	}

	// Check resources
	if got.Linux == nil || got.Linux.Resources == nil {
		t.Fatal("linux.resources is nil")
	}
	if *got.Linux.Resources.Memory.Limit != limit {
		t.Errorf("memory limit: got %d, want %d", *got.Linux.Resources.Memory.Limit, limit)
	}
	if got.Linux.Resources.Pids.Limit != pidsLimit {
		t.Errorf("pids limit: got %d, want %d", got.Linux.Resources.Pids.Limit, pidsLimit)
	}

	// Check namespace count
	if len(got.Linux.Namespaces) != 5 {
		t.Errorf("namespaces: got %d, want 5", len(got.Linux.Namespaces))
	}
}

func TestRootFSPath_Relative(t *testing.T) {
	spec := Spec{
		OCIVersion: "1.0.2",
		Process:    &Process{Args: []string{"/bin/sh"}, Cwd: "/"},
		Root:       &Root{Path: "rootfs"},
	}
	dir := writeTempConfig(t, spec)
	got, _ := LoadSpec(dir)
	want := filepath.Join(dir, "rootfs")
	if got.RootFSPath(dir) != want {
		t.Errorf("RootFSPath: got %q, want %q", got.RootFSPath(dir), want)
	}
}

func TestRootFSPath_Absolute(t *testing.T) {
	spec := Spec{
		OCIVersion: "1.0.2",
		Process:    &Process{Args: []string{"/bin/sh"}, Cwd: "/"},
		Root:       &Root{Path: "/var/lib/myruntime/rootfs"},
	}
	dir := writeTempConfig(t, spec)
	got, _ := LoadSpec(dir)
	want := "/var/lib/myruntime/rootfs"
	if got.RootFSPath(dir) != want {
		t.Errorf("RootFSPath: got %q, want %q", got.RootFSPath(dir), want)
	}
}
