# Orchestrator

## Commands

### Build Template

Build sandbox templates locally or to remote storage.

```bash
sudo go run ./cmd/build-template -build <uuid> -storage .local-build
```

Flags:
- `-build` - Build ID (UUID, required)
- `-storage` - Local path or `gs://bucket` (default: `.local-build`)
- `-from-build` - Base build ID for incremental builds
- `-kernel` - Kernel version (default: `vmlinux-6.1.102`)
- `-kernel-path` - Local kernel binary path (overrides version)
- `-firecracker` - Firecracker version (default: `v1.12.1_717921c`)
- `-firecracker-path` - Local firecracker binary path (overrides version)
- `-envd` - Path to envd binary (default: `../envd/bin/envd`)
- `-vcpu` - vCPUs (default: `1`)
- `-memory` - Memory in MB (default: `512`)
- `-disk` - Disk in MB (default: `1000`)
- `-start-cmd` - Start command
- `-ready-cmd` - Ready check command

### Resume Sandbox

Resume sandboxes from built templates.

```bash
sudo go run ./cmd/resume-sandbox -build <uuid> -from .local-build -iterations 10
```

Flags:
- `-build` - Build ID (UUID, required)
- `-from` - Local path or `gs://bucket` (default: `.local-build`)
- `-iterations` - Number of iterations, 0 = interactive (default: `0`)

### Copy Build

> Works only for GCP buckets right now.

```bash
go run cmd/copy-build/main.go -build <build-id> -from <from-bucket> -to <to-bucket>
```

### Mount Rootfs

> Enable NBD module first:

```bash
modprobe nbd nbds_max=4096

cat <<EOH >/etc/udev/rules.d/97-nbd-device.rules
# Disable inotify watching of change events for NBD devices
ACTION=="add|change", KERNEL=="nbd*", OPTIONS:="nowatch"
EOH
udevadm control --reload-rules
udevadm trigger
```

```bash
./cmd/mount-rootfs/start.sh <bucket> <build-id> <mount-path>
```

### Inspect Header

```bash
TEMPLATE_BUCKET_NAME=<bucket> go run cmd/inspect-header/main.go -build <build-id> -kind <memfile|rootfs>
```

### Inspect Data

```bash
TEMPLATE_BUCKET_NAME=<bucket> go run cmd/inspect-data/main.go -build <build-id> -kind <memfile|rootfs> -start [start] -end [end]
```

---

## Environment Variables

Automatically set in local mode, override for custom paths:

- `ORCHESTRATOR_BASE_PATH` - Base orchestrator data (local: `{storage}/orchestrator`, prod: `/orchestrator`)
- `SNAPSHOT_CACHE_DIR` - Snapshot cache, ideally on NVMe (local: `{storage}/snapshot-cache`, prod: `/mnt/snapshot-cache`)
- `HOST_KERNELS_DIR` - Kernel versions dir (local: `{storage}/kernels`, prod: `/fc-kernels`)
- `FIRECRACKER_VERSIONS_DIR` - Firecracker versions dir (local: `{storage}/fc-versions`, prod: `/fc-versions`)
- `HOST_ENVD_PATH` - Envd binary path (local: `{storage}/envd/envd`, prod: `/fc-envd/envd`)
- `SANDBOX_DIR` - Sandbox working dir (local: `{storage}/sandbox`, prod: `/fc-vm`)
