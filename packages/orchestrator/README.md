# Orchestrator

## Commands

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

### Optimize Build

Run the optimize step on an existing template build. This resumes the template, waits for envd to respond, and collects memory block access patterns to create a prefetch mapping that speeds up future sandbox starts.

```bash
TEMPLATE_BUCKET_NAME=<template-bucket-name> go run cmd/optimize-build/main.go -build <build-id> -vcpu <vcpu-count> -memory <memory-mb>
```

> Required flags:
> - `-build`: The build ID of the existing template to optimize
> - `-vcpu`: Number of vCPUs to use when resuming the sandbox
> - `-memory`: Amount of memory in MB to use when resuming the sandbox
>
> Optional flags:
> - `-kernel`: Kernel version (defaults to version from build metadata)
> - `-firecracker`: Firecracker version (defaults to version from build metadata)
>
> Notes:
> - Hugepages are always enabled
> - Internet access is disabled for the sandbox during optimization
