# Orchestrator

## Commands

> **Prerequisite:** Enable NBD module first:
>
> ```bash
> modprobe nbd nbds_max=4096
>
> cat <<EOH >/etc/udev/rules.d/97-nbd-device.rules
> # Disable inotify watching of change events for NBD devices
> ACTION=="add|change", KERNEL=="nbd*", OPTIONS:="nowatch"
> EOH
> udevadm control --reload-rules
> udevadm trigger
> ```

### Create Build

Create a new build.

```bash
sudo go run ./cmd/create-build -to-build <uuid> -storage .local-build
```

Flags:

- `-to-build <uuid>` - Output build ID (UUID, required)
- `-from-build <uuid>` - Base build ID for incremental builds
- `-template <id>` - Template ID (default: `local-template`)
- `-storage <path>` - Local path or `gs://bucket` (enables local mode with auto-download of kernel/FC)
- `-kernel <version>` - Kernel version (default: `vmlinux-6.1.102`)
- `-firecracker <version>` - Firecracker version (default: `v1.12.1_717921c`)
- `-vcpu <n>` - vCPUs (default: `1`)
- `-memory <mb>` - Memory in MB (default: `512`)
- `-disk <mb>` - Disk in MB (default: `1000`)
- `-hugepages` - Use 2MB huge pages (default: `true`, set `false` for 4KB pages)
- `-start-cmd <cmd>` - Start command
- `-ready-cmd <cmd>` - Ready check command

### Resume Build

Resume a sandbox from a build.

```bash
# Local storage
sudo go run ./cmd/resume-build -from-build <uuid> -storage .local-build -iterations 10

# Remote GCS storage (pass credentials to sudo)
sudo GOOGLE_APPLICATION_CREDENTIALS=$HOME/.config/gcloud/application_default_credentials.json \
  go run ./cmd/resume-build -from-build <uuid> -storage gs://bucket -iterations 10

# Pause mode: resume, run command, then snapshot
sudo go run ./cmd/resume-build -from-build <uuid> -to-build <new-uuid> \
  -storage .local-build -cmd-pause "pip install numpy"
```

Flags:

- `-from-build <uuid>` - Build ID (UUID) to resume from (required)
- `-to-build <uuid>` - Output build ID (UUID) for pause snapshot (auto-generated if not specified)
- `-storage <path>` - Local path or `gs://bucket` (default: `.local-build`)
- `-iterations <n>` - Number of iterations, 0 = interactive (default: `0`)
- `-cold` - Clear cache between iterations (cold start each time)
- `-no-prefetch` - Disable memory prefetching
- `-v` - Verbose logging
- `-pause` - Start and immediately pause (create snapshot)
- `-signal-pause <signal>` - Wait for signal before pause (e.g., `SIGTERM`, `SIGUSR1`)
- `-cmd-pause <cmd>` - Execute command in sandbox, then pause on success

**Pause mode example:**

```bash
# Chain builds with explicit build IDs
sudo go run ./cmd/resume-build -from-build $BUILD1 -to-build $BUILD2 \
  -storage .local-build -cmd-pause "apt install curl"

sudo go run ./cmd/resume-build -from-build $BUILD2 -to-build $BUILD3 \
  -storage .local-build -cmd-pause "pip install requests"
```

### Copy Build

Copy a build between storage locations (local or GCS).

```bash
# Local to local
go run ./cmd/copy-build -build <uuid> -from-storage .local-build -to-storage /other/path

# Local to GCS
go run ./cmd/copy-build -build <uuid> -from-storage .local-build -to-storage gs://bucket

# GCS to GCS
go run ./cmd/copy-build -build <uuid> -from-storage gs://bucket1 -to-storage gs://bucket2
```

Flags:

- `-build <uuid>` - Build ID (UUID, required)
- `-from-storage <path>` - Source: local path or `gs://bucket`
- `-to-storage <path>` - Destination: local path or `gs://bucket`

### Mount Build Rootfs

```bash
# Local storage
sudo go run ./cmd/mount-build-rootfs -build <uuid> -storage .local-build -mount /mnt/rootfs

# Remote GCS storage (pass credentials to sudo)
sudo GOOGLE_APPLICATION_CREDENTIALS=$HOME/.config/gcloud/application_default_credentials.json \
  go run ./cmd/mount-build-rootfs -build <uuid> -storage gs://bucket -mount /mnt/rootfs
```

Flags:

- `-build <uuid>` - Build ID (UUID, required unless `-empty`)
- `-storage <path>` - Local path or `gs://bucket` (default: `.local-build`)
- `-mount <path>` - Mount path
- `-verify` - Verify rootfs integrity (requires `-mount`)
- `-log` - Enable logging
- `-empty` - Create an empty rootfs instead of loading a build
- `-size <bytes>` - Size of empty rootfs (default: 1GB)
- `-block-size <bytes>` - Block size (default: 4096)

### Inspect Build

Inspect build artifacts (headers and data blocks).

```bash
# Inspect memfile header (default)
go run ./cmd/inspect-build -build <uuid> -storage .local-build

# Inspect rootfs header
go run ./cmd/inspect-build -build <uuid> -storage .local-build -rootfs

# Inspect header + data blocks
go run ./cmd/inspect-build -build <uuid> -storage .local-build -data

# Inspect first 100 data blocks
go run ./cmd/inspect-build -build <uuid> -storage .local-build -data -end 100

# Remote GCS storage
go run ./cmd/inspect-build -build <uuid> -storage gs://bucket -rootfs -data
```

Flags:

- `-build <uuid>` - Build ID (UUID, required)
- `-storage <path>` - Local path or `gs://bucket` (default: `.local-build`)
- `-memfile` - Inspect memfile artifact (default)
- `-rootfs` - Inspect rootfs artifact
- `-data` - Also inspect data blocks (not just header)
- `-start <n>` - Start block index (default: `0`, only with `-data`)
- `-end <n>` - End block index (default: all, only with `-data`)

### Show Build Diff

Show the diff between two builds and preview merge result.

```bash
# Memfile (default)
go run ./cmd/show-build-diff -from-build <uuid> -to-build <uuid> -storage .local-build

# Rootfs
go run ./cmd/show-build-diff -from-build <uuid> -to-build <uuid> -storage .local-build -rootfs

# With visualization
go run ./cmd/show-build-diff -from-build <uuid> -to-build <uuid> -storage gs://bucket -visualize
```

Flags:

- `-from-build <uuid>` - Base build ID (required)
- `-to-build <uuid>` - Diff build ID (required)
- `-storage <path>` - Local path or `gs://bucket` (default: `.local-build`)
- `-memfile` - Inspect memfile artifact (default)
- `-rootfs` - Inspect rootfs artifact
- `-visualize` - Visualize the headers

---

## Environment Variables

Automatically set in local mode. Set before running to override:

- `HOST_ENVD_PATH` - Envd binary path (default: `../envd/bin/envd`)
- `HOST_KERNELS_DIR` - Kernel versions dir (local: `{storage}/kernels`, prod: `/fc-kernels`)
- `FIRECRACKER_VERSIONS_DIR` - Firecracker versions dir (local: `{storage}/fc-versions`, prod: `/fc-versions`)
- `ORCHESTRATOR_BASE_PATH` - Base orchestrator data (local: `{storage}/orchestrator`, prod: `/orchestrator`)
- `SNAPSHOT_CACHE_DIR` - Snapshot cache, ideally on NVMe (local: `{storage}/snapshot-cache`, prod: `/mnt/snapshot-cache`)
- `SANDBOX_DIR` - Sandbox working dir (local: `{storage}/sandbox`, prod: `/fc-vm`)
