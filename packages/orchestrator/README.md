# Orchestrator

## Commands

### Build Template (Local)

Build a template locally without remote resources.

```bash
# Prerequisites
sudo modprobe nbd nbds_max=64

cat <<EOH >/etc/udev/rules.d/97-nbd-device.rules
# Disable inotify watching of change events for NBD devices
ACTION=="add|change", KERNEL=="nbd*", OPTIONS:="nowatch"
EOH
udevadm control --reload-rules
udevadm trigger

cd ../envd && make build  # build envd first

# Build (generate a UUID for -build)
sudo `which go` run ./cmd/build-template \
  -local \
  -build $(uuidgen)
```

Options:

- `-local` sets up `.local-build` directory with all artifacts
- `-template` defaults to `local-template`
- `-start-cmd "command"` optional start command to run in sandbox
- `-from-build <uuid>` build from existing template (rebuilds last layer only)

### Resume Sandbox

Resume a sandbox from a previously built template. Useful for testing templates locally.

```bash
# Prerequisites
sudo modprobe nbd nbds_max=64

cat <<EOH >/etc/udev/rules.d/97-nbd-device.rules
# Disable inotify watching of change events for NBD devices
ACTION=="add|change", KERNEL=="nbd*", OPTIONS:="nowatch"
EOH
udevadm control --reload-rules
udevadm trigger

# Resume (keeps running until Ctrl+C)
sudo `which go` run ./cmd/resume-sandbox \
  -local \
  -build <build-id-uuid>

# Benchmark mode: run 10 resume cycles and print stats (min/max/avg/p95/p99)
sudo `which go` run ./cmd/resume-sandbox \
  -local \
  -build <build-id-uuid> \
  -benchmark 10
```

Options: `-vcpu 2` `-memory 512` `-disk 2048` `-benchmark N`

Once running (single mode), SSH into the sandbox from another terminal with the printed command.

### Copy Build

> Works only for GCP buckets right now.

```bash
go run cmd/copy-build/main.go -build <build-id> -from <from-bucket> -to <to-bucket>
```

### Mount Rootfs

> Before calling the script, you need to enable the NBD module in the kernel from root account.

```bash
modprobe nbd nbds_max=4096

cat <<EOH >/etc/udev/rules.d/97-nbd-device.rules
# Disable inotify watching of change events for NBD devices
ACTION=="add|change", KERNEL=="nbd*", OPTIONS:="nowatch"
EOH
udevadm control --reload-rules
udevadm trigger
```

> We need root permissions to use NBD, so we cannot use `go run` directly, but we also need GCP credentials to access the template bucket.

```bash
./cmd/mount-rootfs/start.sh <bucket> <build-id> <mount-path>
```

### Inspect Header

Inspect the header of a build.

```bash
TEMPLATE_BUCKET_NAME=<template-bucket-name> go run cmd/inspect-header/main.go -build <build-id> -kind <kind>
```

> Kind can be `memfile` or `rootfs`.

### Inspect Data

Inspect the data of a build.

```bash
TEMPLATE_BUCKET_NAME=<template-bucket-name> go run cmd/inspect-data/main.go -build <build-id> -kind <kind> -start [start-block] -end [end-block]
```

> Kind can be `memfile` or `rootfs`.
> Start and end block are optional. If not provided, the entire data will be inspected.
