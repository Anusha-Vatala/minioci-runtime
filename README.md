# MiniOCI — Custom OCI Container Runtime

> A lightweight, OCI-inspired container runtime built from scratch in Go.
> Implements Linux namespaces, cgroups v2, OverlayFS, and `pivot_root` — without Docker, runc, or any existing container runtime library.

[![Go Version](https://img.shields.io/badge/Go-1.22-blue)](https://go.dev/)
[![Platform](https://img.shields.io/badge/Platform-Linux%20only-orange)](https://www.kernel.org/)
[![Tests](https://img.shields.io/badge/Tests-passing-brightgreen)](#testing)

---

> [!CAUTION]
> **Linux only.** This project uses Linux-specific syscalls (`pivot_root`, `clone`, `mount`, cgroups v2) and **will not compile or run on Windows or macOS**.
> All commands must be run as **root** (`sudo`). Tested on **Kali Linux**.

---

## Table of Contents

1. [Project Overview](#project-overview)
2. [Objectives](#objectives)
3. [Features](#features)
4. [Architecture / How It Works](#architecture--how-it-works)
5. [Project Structure](#project-structure)
6. [Technologies Used](#technologies-used)
7. [Linux Requirements](#linux-requirements)
8. [Prerequisites](#prerequisites)
9. [Building the Project](#building-the-project)
10. [Creating a Test Root Filesystem](#creating-a-test-root-filesystem)
11. [Creating a Container](#creating-a-container)
12. [Starting a Container](#starting-a-container)
13. [Verifying PID Namespace](#verifying-pid-namespace)
14. [Verifying Hostname Isolation](#verifying-hostname-isolation)
15. [Verifying cgroups v2](#verifying-cgroups-v2)
16. [Verifying OverlayFS](#verifying-overlayfs)
17. [Container Lifecycle](#container-lifecycle)
18. [Example Demo](#example-demo)
19. [Testing](#testing)
20. [Limitations](#limitations)
21. [Why This Project Is Relevant](#why-this-project-is-relevant)
22. [Future Improvements](#future-improvements)
23. [Author](#author)

---

## Project Overview

**MiniOCI** is a custom container runtime written in Go that demonstrates how containers work at the Linux kernel level. It is designed as an educational implementation that closely follows the spirit of the [OCI Runtime Specification](https://github.com/opencontainers/runtime-spec) without importing any existing container runtime library.

The runtime implements the core container lifecycle (`create`, `start`, `state`, `kill`, `delete`), uses Linux namespaces for isolation, cgroups v2 for resource limits, OverlayFS for layered filesystems, and `pivot_root` for filesystem isolation — the same fundamental mechanisms used by production runtimes like `runc`.

> [!NOTE]
> This project is described as **OCI-inspired** rather than fully OCI-compliant. It implements a meaningful subset of the OCI Runtime Specification for educational purposes. It does not pass the full OCI conformance test suite.

**Repository:** https://github.com/Anusha-Vatala/minioci-runtime
**Language:** Go 1.22
**Tested on:** Kali Linux (cgroups v2, overlay kernel module)

---

## Objectives

- Understand how containers are constructed at the Linux kernel level.
- Implement the OCI container lifecycle from scratch without Docker, containerd, or runc.
- Use Linux namespaces (`CLONE_NEWPID`, `CLONE_NEWNS`, `CLONE_NEWUTS`, `CLONE_NEWIPC`, `CLONE_NEWNET`) to isolate processes.
- Apply cgroups v2 resource limits (CPU, memory, PIDs) through the `/sys/fs/cgroup` interface.
- Implement filesystem isolation using `pivot_root` and OverlayFS.
- Build a working CLI tool that can create, start, inspect, kill, and delete containers.

---

## Features

| Feature | Status |
|---|---|
| Container create / start / state / kill / delete lifecycle | ✅ Implemented |
| PID namespace isolation (`CLONE_NEWPID`) | ✅ Implemented & verified |
| Mount namespace isolation (`CLONE_NEWNS`) | ✅ Implemented |
| UTS namespace (hostname) isolation (`CLONE_NEWUTS`) | ✅ Implemented & verified |
| IPC namespace isolation (`CLONE_NEWIPC`) | ✅ Implemented |
| Network namespace isolation (`CLONE_NEWNET`) | ✅ Implemented |
| cgroups v2 — `cpu.max` limit | ✅ Implemented & verified |
| cgroups v2 — `memory.max` limit | ✅ Implemented & verified |
| cgroups v2 — `pids.max` limit | ✅ Implemented & verified |
| `pivot_root` with `chroot` fallback | ✅ Implemented |
| OverlayFS (lowerdir / upperdir / workdir / merged) | ✅ Implemented & verified |
| `/proc` mount inside container | ✅ Implemented |
| OCI-style `config.json` parsing | ✅ Implemented |
| Re-exec pattern for namespace creation | ✅ Implemented |
| On-disk state management (`/run/myruntime/<id>/state.json`) | ✅ Implemented |
| `--detach` mode for `start` | ✅ Implemented |
| `--force` flag for `delete` | ✅ Implemented |
| PID 1 signal handling (SIGTERM, SIGCHLD reaping) | ✅ Implemented |
| User namespace | ⚠️ Code present, not wired to CLI |
| Full OCI conformance | ❌ Not claimed |

---

## Architecture / How It Works

MiniOCI follows the standard two-process model used by production runtimes. The runtime binary acts as **both** the parent container manager **and** the container init process, distinguished by the `init` argument.

```
config.json
    ↓
MiniOCI CLI  (cmd/root.go — parses subcommand, dispatches)
    ↓
OCI-style config parsing  (config/config.go — reads & validates config.json)
    ↓
Linux namespaces  (runtime/namespace.go — CLONE_NEWPID | CLONE_NEWNS | CLONE_NEWUTS | CLONE_NEWIPC | CLONE_NEWNET)
    ↓
cgroups v2  (runtime/cgroup.go — /sys/fs/cgroup/myruntime/<id>/{cpu.max, memory.max, pids.max})
    ↓
mount namespace  (runtime/mount.go — MakeRootPrivate, MountProc, MountSpecMounts)
    ↓
OverlayFS / root filesystem  (runtime/overlay.go — lowerdir=rootfs, upperdir, workdir, merged)
    ↓
pivot_root  (runtime/mount.go — PivotRoot with FallbackChroot)
    ↓
/proc  (runtime/mount.go — MountProc inside rootfs before pivot_root)
    ↓
container process  (runtime/exec.go — syscall.Exec replaces init with /bin/sh or user binary)
```

### Re-execution Pattern

Linux namespaces must be created at `clone(2)` time, before the Go runtime launches goroutines. MiniOCI solves this using the **re-exec pattern** (the same approach used by `runc`):

1. **Parent** (`myruntime start`): forks a child that re-executes `/proc/self/exe` with `"init"` as the first argument and sets `SysProcAttr.Cloneflags` to create the new namespace set at clone time.
2. **Child** (`ContainerInit` in `cmd/init.go`): detects the `"init"` argument before any flag parsing, then calls `RunInitProcess` to complete all namespace setup (`MakeRootPrivate` → `MountOverlay` → `MountProc` → `PivotRoot` → `MountSpecMounts` → `SetHostname` → `syscall.Exec`).

The `InitConfig` struct is passed from parent to child as a JSON-encoded environment variable (`MYRUNTIME_INIT_CONFIG`).

---

## Project Structure

```
MiniOCI — Custom OCI Container Runtime/
├── main.go                      # Entry point; detects "init" arg for re-exec
│
├── cmd/                         # CLI sub-commands
│   ├── root.go                  # Parses global flags; dispatches sub-commands
│   ├── create.go                # `myruntime create`: loads spec, cgroup setup, writes state
│   ├── start.go                 # `myruntime start`: forks child in new namespaces
│   ├── state.go                 # `myruntime state`: prints container state as JSON
│   ├── kill.go                  # `myruntime kill`: sends POSIX signal to container
│   ├── delete.go                # `myruntime delete`: unmounts overlay, removes cgroup & state
│   └── init.go                  # ContainerInit(): runs inside new namespaces (PID 1)
│
├── runtime/                     # Core runtime engine
│   ├── exec.go                  # StartContainer (fork+re-exec), RunInitProcess, WaitForProcess
│   ├── mount.go                 # MakeRootPrivate, MountProc, PivotRoot, FallbackChroot, MountSpecMounts
│   ├── namespace.go             # NamespaceFlags (OCI → CLONE_NEW*), SetHostname, WriteUserMappings
│   ├── cgroup.go                # SetupCgroup, AddProcessToCgroup, RemoveCgroup (cgroups v2)
│   ├── overlay.go               # PrepareOverlayDirs, MountOverlay, UnmountOverlay, IsOverlaySupported
│   ├── state.go                 # SaveState, LoadState, DeleteState, ContainerExists (on-disk state)
│   ├── json_helpers.go          # JSON utility helpers
│   └── state_test.go            # Unit tests for state management
│
├── config/                      # OCI spec parsing
│   ├── config.go                # Spec, Process, Root, Mount, Linux, LinuxResources types; LoadSpec
│   └── config_test.go           # Unit tests for config parsing
│
├── internal/                    # Shared utilities
│   └── logger.go                # Levelled logger (DEBUG/INFO/WARN/ERROR); MYRUNTIME_DEBUG env var
│
├── bundle/                      # Example OCI bundle helpers
│   ├── example_config.json      # Example OCI config.json (Alpine, /bin/sh, 5 namespaces, cgroup limits)
│   └── setup_bundle.sh          # Script to download Alpine Linux mini rootfs and create bundle
│
├── go.mod                       # Module: myruntime, Go 1.22, golang.org/x/sys v0.21.0
└── go.sum                       # Dependency checksums
```

### Package Descriptions

| Package | Purpose |
|---|---|
| `main` | Detects the internal `"init"` re-exec argument before any flag parsing; delegates to `cmd.Execute()` or `cmd.ContainerInit()`. |
| `cmd` | Implements the CLI. Each sub-command is in its own file. `root.go` dispatches by subcommand string. `init.go` runs as PID 1 inside the new namespaces. |
| `runtime/exec` | The heart of container launch: `StartContainer` sets `Cloneflags` to create namespaces and forks the child; `RunInitProcess` completes setup and `exec(2)`s the container binary. |
| `runtime/mount` | All mount namespace operations: making root private, mounting `/proc`, mounting spec-defined filesystems, `pivot_root` with lazy-unmount of the old root, `chroot` fallback. |
| `runtime/namespace` | Converts OCI namespace spec entries to Linux `CLONE_NEW*` bitmask flags. Also handles hostname setting and UID/GID mapping helpers for user namespaces. |
| `runtime/cgroup` | Creates and configures the cgroup v2 hierarchy at `/sys/fs/cgroup/myruntime/<id>/`. Writes `cpu.max`, `memory.max`, `pids.max`, and `cgroup.procs`. |
| `runtime/overlay` | Sets up OverlayFS: prepares directories, mounts the union filesystem, detects kernel support via `/proc/filesystems`. Contains the bug-fixed `IsOverlaySupported`. |
| `runtime/state` | Persists container lifecycle state as `state.json` in `/run/myruntime/<id>/`. Provides `SaveState`, `LoadState`, `DeleteState`, `ContainerExists`. Platform-independent. |
| `config` | Defines Go structs for the OCI `config.json` schema and a `LoadSpec` loader with basic validation. Only the fields actually used by this runtime are defined. |
| `internal` | A simple levelled logger writing to stderr. Debug logging enabled via `MYRUNTIME_DEBUG=1` or the `--debug` global flag. |

---

## Technologies Used

| Technology | Role |
|---|---|
| **Go 1.22** | Implementation language |
| **`golang.org/x/sys`** | Low-level Linux syscalls (`unix.Mount`, `unix.PivotRoot`, `unix.Sethostname`, `unix.CLONE_*` constants) |
| **Linux `clone(2)` / `SysProcAttr.Cloneflags`** | Creating new namespaces at fork time |
| **Linux namespaces** | Process, mount, UTS, IPC, network isolation |
| **cgroups v2 (`/sys/fs/cgroup`)** | CPU, memory, and PID resource limits |
| **OverlayFS** | Copy-on-write layered filesystem (kernel module `overlay`) |
| **`pivot_root(2)`** | Changing root filesystem; more secure than `chroot` |
| **`/proc` (procfs)** | PID namespace visibility; `/proc/self/exe` re-exec |
| **Alpine Linux mini rootfs** | Minimal container root filesystem for testing |

---

## Linux Requirements

> [!IMPORTANT]
> The following kernel features must be available on the host system:

| Requirement | Check Command |
|---|---|
| Linux kernel with namespace support | `uname -r` (>= 3.8 recommended) |
| cgroups v2 unified hierarchy | `stat -fc %T /sys/fs/cgroup` should print `cgroup2fs` |
| OverlayFS kernel module | `grep overlay /proc/filesystems` |
| Root / `CAP_SYS_ADMIN` | All operations must run as root |

```bash
# On Kali Linux — verify cgroups v2:
stat -fc %T /sys/fs/cgroup
# Expected output: cgroup2fs

# Verify OverlayFS support:
grep overlay /proc/filesystems
# Expected output:
# nodev   overlay
```

---

## Prerequisites

- **OS:** Linux (tested on Kali Linux). Does not work on Windows or macOS.
- **Go:** 1.22 or later — [install Go](https://go.dev/doc/install)
- **Root access:** `sudo` for all runtime commands
- **OverlayFS kernel module:** loaded (usually built-in on modern distros)
- **`curl` or `wget`:** for the bundle setup script to download Alpine Linux

---

## Building the Project

> [!NOTE]
> Build must be done on a Linux system. The source files use `//go:build linux` constraints.

```bash
# On the Kali Linux host:
git clone https://github.com/Anusha-Vatala/minioci-runtime.git
cd "MiniOCI — Custom OCI Container Runtime"

# Build the runtime binary:
go build -o myruntime .
```

Verify:

```bash
./myruntime help
```

Expected output:

```
myruntime — lightweight OCI-inspired container runtime

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
```

---

## Creating a Test Root Filesystem

A container needs a root filesystem (rootfs). The provided `bundle/setup_bundle.sh` script downloads the Alpine Linux mini rootfs and creates the full OCI bundle directory structure automatically.

```bash
# On the Kali Linux host:
sudo bash bundle/setup_bundle.sh ~/minioci-test
```

This script:
1. Creates `~/minioci-test/rootfs/` and extracts Alpine Linux into it.
2. Creates the OverlayFS directories: `overlay/lower`, `overlay/upper`, `overlay/work`, `overlay/merged`.
3. Copies `bundle/example_config.json` to `~/minioci-test/config.json`.

After running the script, the bundle layout is:

```
~/minioci-test/
├── config.json          <- OCI spec (process, root, namespaces, resources)
├── rootfs/              <- Alpine Linux root filesystem
│   ├── bin/
│   ├── etc/
│   ├── proc/            <- empty; /proc will be mounted here
│   └── ...
└── overlay/
    ├── lower/           <- (empty; rootfs is used as lowerdir)
    ├── upper/           <- writable layer (container writes appear here on host)
    ├── work/            <- OverlayFS internal scratch space
    └── merged/          <- union mount point (the container's rootfs view)
```

---

## Creating a Container

> [!WARNING]
> All `myruntime` commands require `sudo`.

```bash
# Standard create (no OverlayFS):
sudo ./myruntime create --bundle ~/minioci-test mybox

# Create with OverlayFS enabled:
sudo ./myruntime create --bundle ~/minioci-test --overlay mybox
```

Expected output (standard create):

```
[create] loading config.json from /root/minioci-test
[create] setting up cgroup for container "mybox"
[create] container "mybox" created successfully
         bundle:  /root/minioci-test
         rootfs:  /root/minioci-test/rootfs
         overlay: false
  Run 'myruntime start mybox' to start the container.
```

Expected output (with `--overlay`):

```
[create] loading config.json from /root/minioci-test
[create] using rootfs as overlayfs lowerdir: /root/minioci-test/rootfs
[create] overlayfs prepared, merged dir: /root/minioci-test/overlay/merged
[create] setting up cgroup for container "mybox"
[create] container "mybox" created successfully
         bundle:  /root/minioci-test
         rootfs:  /root/minioci-test/overlay/merged
         overlay: true
  Run 'myruntime start mybox' to start the container.
```

What `create` does internally:

1. Reads and validates `config.json` from the bundle directory.
2. If `--overlay` is specified, prepares the OverlayFS directory structure and uses `overlay/merged` as the effective rootfs.
3. Creates the cgroup hierarchy at `/sys/fs/cgroup/myruntime/mybox/` and writes `cpu.max`, `memory.max`, `pids.max`.
4. Writes the initial `state.json` (status: `created`) to `/run/myruntime/mybox/state.json`.

---

## Starting a Container

```bash
# On the Kali Linux host:
sudo ./myruntime start mybox
```

Expected output:

```
[start] launching container "mybox"
        rootfs:  /root/minioci-test/rootfs
        process: [/bin/sh]
[start] container "mybox" running with PID 12345
[start] waiting for container to exit (Ctrl-C to detach)...
```

You are now inside the container shell (`/bin/sh`). You will see a prompt such as `/ #`.

What `start` does internally:

1. Loads the `created` state from `/run/myruntime/mybox/state.json`.
2. Re-reads `config.json` from the bundle directory.
3. Forks a child that re-executes `/proc/self/exe init` with `SysProcAttr.Cloneflags` set to create new PID, mount, UTS, IPC, and network namespaces.
4. The child runs `RunInitProcess`: makes root private → (optionally mounts OverlayFS) → mounts `/proc` → calls `pivot_root` → mounts spec filesystems → sets hostname → `exec`s `/bin/sh`.
5. Writes the child's host PID into `cgroup.procs` to apply resource limits.
6. Updates state to `running`.

---

## Verifying PID Namespace

**Inside the container** (after `start`):

```sh
# Inside the container:
echo $$
```

Expected output:

```
1
```

The container init process sees itself as PID 1 in its own isolated PID namespace, even though it has a different PID on the host. This was verified during testing.

---

## Verifying Hostname Isolation

**Inside the container** (after `start`):

```sh
# Inside the container:
hostname
```

Expected output:

```
mycontainer
```

> [!NOTE]
> The hostname is read from `config.json` → `"hostname"` field. The example config sets it to `"mycontainer"`. The `SetHostname` function uses `unix.Sethostname` inside the UTS namespace, so it does not affect the host's hostname.

Confirm the host hostname is unchanged in a separate terminal:

```bash
# On the Kali Linux host (separate terminal):
hostname
# Returns your actual host hostname, unchanged.
```

---

## Verifying cgroups v2

After `start`, the container process is added to its cgroup. Verify the limits from the **Kali Linux host**:

```bash
sudo cat /sys/fs/cgroup/myruntime/mybox/cpu.max
```

Expected output (100ms quota per 100ms period):

```
100000 100000
```

```bash
sudo cat /sys/fs/cgroup/myruntime/mybox/memory.max
```

Expected output (256 MiB = 268,435,456 bytes):

```
268435456
```

```bash
sudo cat /sys/fs/cgroup/myruntime/mybox/pids.max
```

Expected output:

```
64
```

These values come directly from the `linux.resources` section of `config.json`:

```json
"resources": {
  "memory": { "limit": 268435456 },
  "cpu":    { "quota": 100000, "period": 100000 },
  "pids":   { "limit": 64 }
}
```

---

## Verifying OverlayFS

Start the container with `--overlay` and create a file inside:

```bash
# On the Kali Linux host:
sudo ./myruntime create --bundle ~/minioci-test --overlay mybox
sudo ./myruntime start mybox
```

**Inside the container:**

```sh
# Inside the container:
ls /
echo "overlay-test" > /tmp/test.txt
cat /tmp/test.txt
```

Expected:

```
overlay-test
```

**Exit the container** and verify on the host:

```bash
# On the Kali Linux host:
sudo find ~/minioci-test/overlay/upper -type f
```

Expected output:

```
/root/minioci-test/overlay/upper/tmp/test.txt
```

This confirms OverlayFS copy-on-write is working: the file written inside the container appears in `overlay/upper` on the host, while the original `rootfs` (lowerdir) remains unmodified.

---

## Container Lifecycle

MiniOCI implements the OCI-style container lifecycle. The following diagram shows the state transitions:

```
  myruntime create
        |
        v
   [ created ]  -- myruntime start -->  [ running ]
                                              |
                                    myruntime kill
                                     (SIGTERM/SIGKILL)
                                              |
                                              v
                                        [ stopped ]
                                              |
                                    myruntime delete
                                              |
                                              v
                                     (state removed)
```

| Command | Description |
|---|---|
| `myruntime create <id> --bundle <dir>` | Parses config, sets up cgroup, writes `state.json` (status: `created`) |
| `myruntime start <id>` | Forks child in new namespaces, exec's container process (status: `running`) |
| `myruntime state <id>` | Prints current `state.json` as formatted JSON to stdout |
| `myruntime kill <id> [signal]` | Sends signal to container process (default: SIGTERM) |
| `myruntime delete <id>` | Unmounts OverlayFS, removes cgroup, deletes state files |

Additional flags:

```bash
myruntime start mybox --detach      # Run container in background
myruntime delete mybox --force      # Force-kill running container then delete
myruntime --debug create ...        # Enable verbose debug logging
```

State is persisted on disk at `/run/myruntime/<container-id>/state.json`. This is a tmpfs on most Linux systems and does not survive reboots.

---

## Example Demo

A complete end-to-end demo using OverlayFS:

```bash
# Step 1: Build
go build -o myruntime .

# Step 2: Set up the bundle
sudo bash bundle/setup_bundle.sh ~/minioci-test

# Step 3: Create the container
sudo ./myruntime create --bundle ~/minioci-test --overlay mybox

# Step 4: Start the container
sudo ./myruntime start mybox
# You are now inside the container.
```

**Commands inside the container:**

```sh
# Verify PID isolation — should print "1"
echo $$

# Verify hostname isolation — should print "mycontainer"
hostname

# Explore the isolated filesystem
ls /

# Test OverlayFS write isolation
echo "overlay-test" > /tmp/test.txt
cat /tmp/test.txt

# Exit the container
exit
```

**Back on the Kali Linux host** (after the container exits):

```bash
# Verify cgroups v2 limits were applied:
sudo cat /sys/fs/cgroup/myruntime/mybox/cpu.max
sudo cat /sys/fs/cgroup/myruntime/mybox/memory.max
sudo cat /sys/fs/cgroup/myruntime/mybox/pids.max

# Verify OverlayFS write isolation:
sudo find ~/minioci-test/overlay/upper -type f

# Check final container state:
sudo ./myruntime state mybox

# Clean up:
sudo ./myruntime delete mybox
```

---

## Testing

Run the test suite (no root required for unit tests):

```bash
go test ./...
```

Actual test results:

```
?       myruntime       [no test files]
?       myruntime/cmd   [no test files]
ok      myruntime/config
?       myruntime/internal      [no test files]
ok      myruntime/runtime
```

- **`myruntime/config`**: Tests for `config.go` — spec loading, field parsing, validation, `RootFSPath` resolution.
- **`myruntime/runtime`**: Tests for `state.go` — `SaveState`, `LoadState`, `DeleteState`, `ContainerExists`, `ListContainers`.

> [!NOTE]
> Integration tests (namespace creation, cgroup writes, OverlayFS mounts) require a Linux host with root and are verified manually as described in the sections above.

---

## Limitations

- **Linux-only**: Uses Linux-specific syscalls and will not run on Windows or macOS.
- **Root required**: Namespace creation, cgroup writes, and mount syscalls all require `CAP_SYS_ADMIN`. Rootless operation is not implemented.
- **No networking**: A new network namespace is created but only a loopback interface exists inside. No veth pairs, bridges, or NAT are configured.
- **No image management**: The runtime reads a pre-existing bundle directory. It does not pull, unpack, or manage container images.
- **Not fully OCI-compliant**: This is an OCI-inspired educational runtime. It does not implement hooks, masked/read-only paths enforcement, seccomp, AppArmor, or the full OCI state machine.
- **Foreground by default**: `start` runs the container in the foreground by default. Use `--detach` to background it.
- **State in tmpfs**: Container state is stored in `/run/myruntime/` which does not survive reboots.
- **Single container per invocation**: There is no daemon process. Each `start` command manages exactly one container.

---

## Why This Project Is Relevant

Understanding how containers work at the kernel level is an essential skill for systems programming, cloud infrastructure, and DevOps engineering.

**This project demonstrates:**

- **Linux internals knowledge**: Direct use of `clone(2)`, `mount(2)`, `pivot_root(2)`, `sethostname(2)`, cgroups v2 filesystem interface, and procfs — the same primitives Docker and Kubernetes rely on.
- **Go systems programming**: Practical use of `syscall`, `os/exec`, `golang.org/x/sys/unix`, and safe cross-process communication via environment variables.
- **OCI ecosystem understanding**: Implementation of the OCI container lifecycle (`create` → `start` → `state` → `kill` → `delete`) familiar to anyone working with `runc`, `containerd`, or Kubernetes CRI.
- **Security reasoning**: Understanding *why* `pivot_root` is preferred over `chroot`, *why* the root mount must be made private before container mounts, and *why* PID 1 in a namespace requires explicit signal handling.
- **Software design**: The re-exec pattern for safe namespace creation, separation of concerns across packages, on-disk state management, and test coverage for platform-independent code.

---

## Future Improvements

- [ ] **Network setup**: Add veth pair creation and NAT so containers can reach the internet.
- [ ] **Rootless containers**: Support user namespace UID/GID remapping to run containers without root.
- [ ] **Image layer management**: Pull and unpack OCI image layers to construct the rootfs automatically.
- [ ] **Container-to-container networking**: Implement a bridge network for multi-container communication.
- [ ] **Seccomp / AppArmor**: Apply the security profiles defined in `config.json`.
- [ ] **Masked and read-only paths**: Enforce `maskedPaths` and `readonlyPaths` from the OCI spec.
- [ ] **OCI hooks**: Implement `prestart`, `poststart`, and `poststop` lifecycle hooks.
- [ ] **Persistent state**: Move container state out of tmpfs to survive reboots.
- [ ] **Integration tests**: Automated tests that verify namespace isolation and cgroup limits.
- [ ] **Multiple rootfs formats**: Support tar archives and OCI image layout as bundle sources.

---

## Author

**Anusha Vatala**
GitHub: [@Anusha-Vatala](https://github.com/Anusha-Vatala)
Repository: [minioci-runtime](https://github.com/Anusha-Vatala/minioci-runtime)

---

*Built as a deep-dive systems programming project to understand how Linux containers work at the kernel level — without relying on Docker, runc, or any existing container runtime.*
