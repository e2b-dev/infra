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
