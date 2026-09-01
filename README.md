# myruntime — Lightweight OCI-Inspired Container Runtime

A from-scratch container runtime written in Go that demonstrates how containers
actually work under the hood — using Linux namespaces, cgroups v2, pivot_root,
and OverlayFS — without Docker, runc, Podman, or any existing container runtime
library.

> **Target platform:** Linux (Kali Linux / Ubuntu 22.04+) with cgroups v2.  
> **Development platform:** Windows (source editing) → cross-compiled or built on Linux VM.  
> **Requires root** for most runtime operations.

---

## Architecture Diagram

```
┌─────────────────────────────────────────────────────────────────────┐
│                        myruntime binary                             │
│                                                                     │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────┐ │
│  │  create  │  │  start   │  │  state   │  │   kill   │  │delete│ │
│  └────┬─────┘  └────┬─────┘  └────┬─────┘  └────┬─────┘  └──┬───┘ │
│       │             │              │              │            │     │
│       ▼             ▼              ▼              ▼            ▼     │
│  ┌────────────────────────────────────────────────────────────────┐ │
│  │                      runtime/ package                          │ │
│  │                                                                │ │
│  │  config.go ──► LoadSpec()     state.go  ──► SaveState()       │ │
│  │  cgroup.go ──► SetupCgroup()  exec.go   ──► StartContainer()  │ │
│  │  mount.go  ──► PivotRoot()    namespace ──► NamespaceFlags()  │ │
│  │  overlay.go──► MountOverlay()                                  │ │
│  └────────────────────────────────────────────────────────────────┘ │
└─────────────────────────────────────────────────────────────────────┘
         │ fork-exec /proc/self/exe init
         ▼
┌─────────────────────────────────────────────────────────────────────┐
│             Child Process (inside new namespaces)                   │
│                                                                     │
│   ContainerInit()                                                   │
│      │                                                              │
│      ├─ MakeRootPrivate()    (MS_PRIVATE|MS_REC on /)              │
│      ├─ MountOverlay()       (overlayfs lower/upper/work → merged) │
│      ├─ MountProc()          (/proc inside rootfs)                 │
│      ├─ PivotRoot()          (pivot_root or chroot fallback)       │
│      ├─ MountSpecMounts()    (/dev, /sys, /tmp from config.json)   │
│      ├─ SetHostname()        (UTS namespace)                       │
│      ├─ Chdir(cwd)                                                 │
│      └─ syscall.Exec()       (replace process with container cmd)  │
└─────────────────────────────────────────────────────────────────────┘

Linux Kernel Resources
──────────────────────
  Namespaces:  PID  MOUNT  UTS  IPC  NETWORK  (USER optional)
  Cgroups v2:  /sys/fs/cgroup/myruntime/<id>/
               ├─ cpu.max
               ├─ memory.max
               ├─ pids.max
               └─ cgroup.procs
  Filesystem:  OverlayFS (lower=image, upper=rw, work=scratch, merged=view)
  State:       /run/myruntime/<id>/state.json
```

---

## Project Structure

```
myruntime/
├── main.go                    # Entry point; detects "init" re-exec path
├── go.mod                     # Go module (requires golang.org/x/sys)
├── go.sum
│
├── cmd/
│   ├── root.go                # CLI dispatcher, usage text
│   ├── create.go              # `create` sub-command (Linux)
│   ├── start.go               # `start` sub-command (Linux)
│   ├── state.go               # `state` sub-command
│   ├── kill.go                # `kill` sub-command (Linux)
│   ├── delete.go              # `delete` sub-command (Linux)
│   └── init.go                # Container init entry point (Linux, PID-1 signal handling)
│
├── runtime/
│   ├── state.go               # Container state (create/load/delete state.json)
│   ├── state_test.go          # Unit tests for state structures
│   ├── json_helpers.go        # Shared JSON encode/decode helpers
│   ├── namespace.go           # Linux namespace flags + hostname + UID/GID maps
│   ├── cgroup.go              # cgroup v2 setup, limits, cleanup
│   ├── mount.go               # pivot_root, chroot, proc/sysfs/tmpfs mounts
│   ├── overlay.go             # OverlayFS setup and cleanup
│   └── exec.go                # StartContainer (fork-exec) + RunInitProcess
│
├── config/
│   ├── config.go              # OCI spec structs + LoadSpec() + validation
│   └── config_test.go         # Unit tests (platform-independent)
│
├── internal/
│   └── logger.go              # Levelled logger writing to stderr
│
└── bundle/
    ├── example_config.json    # Sample OCI config.json
    └── setup_bundle.sh        # Downloads Alpine rootfs, creates bundle
```

---

## Prerequisites

### Linux VM (runtime host)

```bash
# Ubuntu / Kali
sudo apt-get update
sudo apt-get install -y golang-go git curl

# Verify cgroups v2
cat /sys/fs/cgroup/cgroup.controllers
# Should output something like: cpuset cpu io memory hugetlb pids rdma misc

# Verify overlay module
grep overlay /proc/filesystems
# Should show: nodev overlay
# If missing: sudo modprobe overlay

# Verify Go version (need 1.22+)
go version
```

### Windows (source editing only)

No special setup needed — edit source files in Antigravity or any editor.
The Linux-specific files use `//go:build linux` tags so they won't cause
compile errors on Windows. Unit tests for platform-independent components
(`config/`, `runtime/state.go`) can be run on Windows.

---

## Build

### On Linux

```bash
# Clone / navigate to the project
cd myruntime

# Download dependencies
go mod tidy

# Build the binary
go build -o myruntime .

# Verify
./myruntime help
```

### Cross-compile on Windows (for Linux)

```powershell
# In PowerShell or Command Prompt:
$env:GOOS = "linux"
$env:GOARCH = "amd64"
go build -o myruntime .
# Transfer myruntime binary to your Linux VM
```

---

## Setup: Create a Test Bundle

```bash
# Run the setup script (downloads Alpine Linux 3.19 minirootfs)
sudo bash bundle/setup_bundle.sh /tmp/myruntime-bundle

# Verify the structure:
ls /tmp/myruntime-bundle/
# config.json  overlay/  rootfs/

ls /tmp/myruntime-bundle/rootfs/
# bin  dev  etc  home  lib  media  mnt  opt  proc  root  run  sbin  srv  sys  tmp  usr  var
```

---

## Usage

All commands require **root** on Linux.

### Create a Container

```bash
# Minimal (no overlay):
sudo ./myruntime create mybox --bundle /tmp/myruntime-bundle

# With OverlayFS (container writes go to overlay/upper, image stays clean):
sudo ./myruntime create mybox --bundle /tmp/myruntime-bundle --overlay
```

### Start the Container

```bash
# Foreground (interactive — waits for the container process to exit):
sudo ./myruntime start mybox

# Background:
sudo ./myruntime start mybox --detach
```

### Check Container State

```bash
sudo ./myruntime state mybox
```

Output (JSON):
```json
{
  "ociVersion": "1.0.2",
  "id": "mybox",
  "status": "running",
  "pid": 12345,
  "bundle": "/tmp/myruntime-bundle",
  "created": "2024-06-01T12:00:00Z"
}
```

### Send a Signal

```bash
# Graceful shutdown:
sudo ./myruntime kill mybox SIGTERM

# Force kill:
sudo ./myruntime kill mybox SIGKILL
```

### Delete the Container

```bash
# Only works when container is stopped:
sudo ./myruntime delete mybox

# Force-kill and delete:
sudo ./myruntime delete mybox --force
```

---

## Step-by-Step Testing Walkthrough

```bash
# === 1. Build ===
go mod tidy
go build -o myruntime .

# === 2. Set up bundle ===
sudo bash bundle/setup_bundle.sh /tmp/myruntime-bundle

# === 3. Verify cgroup v2 and overlay ===
cat /sys/fs/cgroup/cgroup.controllers
grep overlay /proc/filesystems

# === 4. Create container ===
sudo ./myruntime create mybox --bundle /tmp/myruntime-bundle
# Expected: container "mybox" created successfully

# === 5. Check state (should be "created") ===
sudo ./myruntime state mybox

# === 6. Inspect cgroup was created ===
ls /sys/fs/cgroup/myruntime/mybox/
# Expected: cgroup.controllers  cgroup.procs  cpu.max  memory.max  pids.max

cat /sys/fs/cgroup/myruntime/mybox/cpu.max
# Expected: 100000 100000

cat /sys/fs/cgroup/myruntime/mybox/memory.max
# Expected: 134217728

cat /sys/fs/cgroup/myruntime/mybox/pids.max
# Expected: 64

# === 7. Start container (runs /bin/sh interactively) ===
sudo ./myruntime start mybox
# You should get a shell prompt inside the container.
# Try:
#   hostname          → should show "mycontainer"
#   cat /proc/1/comm  → should show "sh" (or your process)
#   ps aux            → should only show processes inside the PID namespace
#   ls /              → Alpine root filesystem
#   exit              → exits the container

# === 8. Check state post-exit ===
sudo ./myruntime state mybox
# Expected: "status": "stopped"

# === 9. Delete ===
sudo ./myruntime delete mybox
# Expected: container "mybox" deleted successfully

# === 10. Verify cleanup ===
ls /sys/fs/cgroup/myruntime/ 2>/dev/null && echo "still exists" || echo "cleaned up"
ls /run/myruntime/ 2>/dev/null && echo "still exists" || echo "cleaned up"

# === 11. Test OverlayFS ===
sudo ./myruntime create mybox-overlay --bundle /tmp/myruntime-bundle --overlay
sudo ./myruntime start mybox-overlay
# Inside container:
#   touch /testfile             → creates file in overlay upper dir
#   exit
# On host:
ls /tmp/myruntime-bundle/overlay/upper/
# Should show testfile (changes are in upper, lower is unchanged)
sudo ./myruntime delete mybox-overlay

# === 12. Run unit tests (platform-independent, no root needed) ===
go test ./config/... -v
go test ./runtime/ -run TestState -v
```

---

## Linux Concepts Explained

### Namespaces

| Namespace | Clone Flag      | Isolates                                      |
|-----------|-----------------|-----------------------------------------------|
| PID       | CLONE_NEWPID    | Process ID tree. Container process = PID 1.   |
| Mount     | CLONE_NEWNS     | Filesystem mount table.                       |
| UTS       | CLONE_NEWUTS    | Hostname and domain name.                     |
| IPC       | CLONE_NEWIPC    | System V IPC, POSIX message queues.           |
| Network   | CLONE_NEWNET    | Network interfaces, routing, iptables.        |
| User      | CLONE_NEWUSER   | UID/GID ranges. Allows rootless containers.   |

### cgroups v2

Control Groups limit what resources a process group can consume:

```
/sys/fs/cgroup/myruntime/<container-id>/
├── cpu.max          "100000 100000"  = 100ms quota per 100ms period (1 CPU)
├── memory.max       134217728         = 128 MiB
├── pids.max         64                = max 64 processes
└── cgroup.procs     <pid>             = add container PID here
```

### pivot_root vs chroot

- **chroot(2)**: Changes the apparent root for path resolution. Can be escaped
  by a process with CAP_SYS_CHROOT. Simpler but less secure.
- **pivot_root(2)**: Moves the current root mount to a subdirectory and makes
  a new directory the root mount. The old root becomes unreachable (we unmount it).
  Requires CLONE_NEWNS (mount namespace). More secure.

### OverlayFS

```
lowerdir (read-only image)
    ↕  (kernel merges these)
upperdir (read-write changes)
────────────────────────────
merged   (what the container sees)
```

Writes go to `upperdir`. Reads prefer `upperdir` over `lowerdir`. The image
(`lowerdir`) is never modified — perfect for running multiple containers from
the same image.

### Re-execution Pattern

The Go runtime starts threads before `main()`, which prevents using `unshare(2)`
safely. Instead:

1. **Parent** starts a child via `exec.Cmd` with `Cloneflags` (creates namespaces
   at the kernel `clone()` call, before Go threads start in the child).
2. **Child** receives `"init"` as its first argument, detects it, and completes
   namespace setup before execing the container binary.

---

## Troubleshooting

### `pivot_root: invalid argument`
```bash
# Ensure you have a mount namespace:
# config.json must include {"type": "mount"} in linux.namespaces
# pivot_root requires both new_root and put_old to be on different filesystems.
# The runtime does a self-bind-mount on newRoot to satisfy this.
```

### `overlay: no such device` or `mount: unknown filesystem type 'overlay'`
```bash
sudo modprobe overlay
grep overlay /proc/filesystems   # should now show "nodev overlay"
```

### `cgroup.procs: no such file or directory`
```bash
# Cgroup v2 not active or myruntime cgroup not created.
# Check:
cat /proc/mounts | grep cgroup
# Should show: cgroup2 /sys/fs/cgroup cgroup2 ...
# If you see cgroup (v1), enable cgroup v2:
#   Add to GRUB_CMDLINE_LINUX in /etc/default/grub:
#   systemd.unified_cgroup_hierarchy=1
#   then: sudo update-grub && sudo reboot
```

### `operation not permitted` on mount
```bash
# Ensure you are running as root:
sudo ./myruntime create ...
sudo ./myruntime start ...
```

### Container exits immediately
```bash
# Check if /bin/sh exists in the rootfs:
ls /tmp/myruntime-bundle/rootfs/bin/sh

# Enable debug logging:
sudo ./myruntime --debug start mybox

# Check the container stderr output for [myruntime/init] messages.
```

### `permission denied` writing to `/run/myruntime`
```bash
sudo mkdir -p /run/myruntime
sudo chmod 700 /run/myruntime
# Or just run myruntime as root (sudo).
```

---

## Unit Tests

Platform-independent tests run on Windows and Linux without root:

```bash
# OCI spec parser tests:
go test ./config/... -v

# State management tests:
go test ./runtime/ -run TestState -v

# All tests:
go test ./...
```

Linux-only integration (requires root and cgroups v2):

```bash
# Manual testing following the walkthrough above.
```

---

## Security Considerations

1. **Root required**: Creating namespaces and writing to cgroups requires
   `CAP_SYS_ADMIN`. For production use, consider user namespaces for
   privilege dropping.

2. **No seccomp**: This runtime does not apply a seccomp (syscall filter) policy.
   A production runtime would restrict the syscalls available to container processes.

3. **No capability dropping**: We don't drop Linux capabilities from the container
   process. A real runtime would drop all capabilities except a safe minimal set.

4. **chroot escape**: If `pivot_root` falls back to `chroot`, a privileged process
   inside the container could potentially escape. Always use `pivot_root` in production.

5. **Test in a disposable VM**: Since this runtime runs as root and manipulates
   mounts and cgroups, always test in a VM you can snapshot/restore.

---

## License

MIT — see LICENSE file.
