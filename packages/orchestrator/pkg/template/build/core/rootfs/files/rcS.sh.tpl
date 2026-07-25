{{- /*gotype:github.com/e2b-dev/infra/packages/orchestrator/pkg/template/build/core/rootfs.templateModel*/ -}}
{{ .WriteFile "etc/init.d/rcS" 0o777 }}

#!/usr/bin/busybox ash
# Every command goes through the baked busybox: this runs before provisioning
# on the raw base image, and bare images (premade NixOS, distroless) have no
# mkdir/mount on PATH at all (FEAT-145).
BB=/usr/bin/busybox

echo "Mounting essential filesystems"
# Ensure necessary mount points exist
$BB mkdir -p /proc /sys /dev /tmp /run

# Mount essential filesystems
$BB mount -t proc proc /proc
$BB mount -t sysfs sysfs /sys
$BB mount -t devtmpfs devtmpfs /dev
$BB mount -t tmpfs tmpfs /tmp
$BB mount -t tmpfs tmpfs /run

echo "System Init"
