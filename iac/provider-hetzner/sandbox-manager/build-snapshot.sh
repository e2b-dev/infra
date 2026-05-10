#!/usr/bin/env bash
# Snapshot builder v2 — with vsock + socat exec-listener
set -e
SOCK=/tmp/fc-template-build-v2.sock
WORK=/var/lib/maxicore-sandbox
SNAP_DIR=$WORK/snapshots
TEMPL_ROOTFS=$WORK/template-rootfs.ext4
SOURCE_ROOTFS=/opt/firecracker/rootfs/ubuntu-22.04.ext4
KERNEL=/opt/firecracker/kernels/vmlinux-6.1.141
VSOCK_PATH=/tmp/maxicore-vsock-template
VSOCK_PORT=10000

# Prerequisite check
which socat || apt-get install -y -q socat
mkdir -p $SNAP_DIR

# Force rebuild
rm -f $SNAP_DIR/template.snap $SNAP_DIR/template.mem
echo "[1/8] Copy rootfs source..."
cp -f $SOURCE_ROOTFS $TEMPL_ROOTFS

echo "[2/8] Inject vsock-exec init script via loop-mount..."
MOUNT=$(mktemp -d)
mount -o loop $TEMPL_ROOTFS $MOUNT

# Check socat in source rootfs (Ubuntu 22.04 has it usually)
if [ ! -f "$MOUNT/usr/bin/socat" ]; then
    echo "  socat missing in rootfs, copying from host"
    cp /usr/bin/socat $MOUNT/usr/bin/socat || true
fi

cat > $MOUNT/maxicore-init.sh <<'INIT'
#!/bin/sh
mount -t proc proc /proc 2>/dev/null
mount -t sysfs sys /sys 2>/dev/null
mount -t tmpfs tmpfs /tmp 2>/dev/null
mkdir -p /tmp/maxicore
echo "READY at $(date -u +%H:%M:%S.%N)" > /tmp/maxicore/ready
echo "BOOT_UPTIME: $(cat /proc/uptime | awk '{print $1}')s" >> /tmp/maxicore/ready
hostname maxicore-vm-$$ 2>/dev/null

# Vsock exec-listener: receive cmd via vsock-port-10000, exec via sh, return output
# Pattern: each connection = single command + exit
exec /usr/bin/socat -d -d VSOCK-LISTEN:10000,fork EXEC:'/bin/sh',stderr,pty,setsid >/tmp/maxicore/vsock.log 2>&1
INIT
chmod +x $MOUNT/maxicore-init.sh
sync
umount $MOUNT
rmdir $MOUNT

echo "[3/8] Start Firecracker template VM..."
rm -f $SOCK
firecracker --api-sock $SOCK > $WORK/template-fc-v2.log 2>&1 &
FC_PID=$!
sleep 0.5

echo "[4/8] Configure VM (with vsock device)..."
curl -sf --unix-socket $SOCK -X PUT 'http://localhost/machine-config' \
    -H 'Content-Type: application/json' \
    -d '{"vcpu_count": 2, "mem_size_mib": 512}'
curl -sf --unix-socket $SOCK -X PUT 'http://localhost/boot-source' \
    -H 'Content-Type: application/json' \
    -d "{\"kernel_image_path\":\"$KERNEL\",\"boot_args\":\"console=ttyS0 reboot=k panic=1 pci=off init=/maxicore-init.sh\"}"
curl -sf --unix-socket $SOCK -X PUT 'http://localhost/drives/rootfs' \
    -H 'Content-Type: application/json' \
    -d "{\"drive_id\":\"rootfs\",\"path_on_host\":\"$TEMPL_ROOTFS\",\"is_root_device\":true,\"is_read_only\":false}"

# Add vsock device — uds_path is host-side socket, guest_cid identifies the VM
curl -sf --unix-socket $SOCK -X PUT 'http://localhost/vsock' \
    -H 'Content-Type: application/json' \
    -d "{\"vsock_id\":\"vsock0\",\"guest_cid\":3,\"uds_path\":\"$VSOCK_PATH\"}"

echo "[5/8] InstanceStart..."
curl -sf --unix-socket $SOCK -X PUT 'http://localhost/actions' \
    -H 'Content-Type: application/json' \
    -d '{"action_type":"InstanceStart"}'
sleep 4  # wait for socat to bind

echo "[6/8] Test vsock connection (sanity)..."
ls -la "${VSOCK_PATH}_10000" 2>&1 | head -3 || echo "  vsock UDS port-socket not created (may use different naming)"
ls -la $VSOCK_PATH 2>&1 | head -3

echo "[7/8] Pause VM + Snapshot..."
curl -sf --unix-socket $SOCK -X PATCH 'http://localhost/vm' -H 'Content-Type: application/json' -d '{"state":"Paused"}'
curl -sf --unix-socket $SOCK -X PUT 'http://localhost/snapshot/create' \
    -H 'Content-Type: application/json' \
    -d "{\"snapshot_type\":\"Full\",\"snapshot_path\":\"$SNAP_DIR/template.snap\",\"mem_file_path\":\"$SNAP_DIR/template.mem\"}"
SNAP_RC=$?
echo

echo "[8/8] Cleanup template VM..."
kill $FC_PID 2>/dev/null || true
wait $FC_PID 2>/dev/null || true
rm -f $SOCK $VSOCK_PATH ${VSOCK_PATH}_*

if [ $SNAP_RC -eq 0 ] && [ -f "$SNAP_DIR/template.snap" ] && [ -f "$SNAP_DIR/template.mem" ]; then
    echo "✅ V2 SNAPSHOT BUILD SUCCESS (with vsock + socat-exec)"
    ls -lh $SNAP_DIR/template.*
    exit 0
else
    echo "❌ V2 SNAPSHOT BUILD FAILED"
    exit 1
fi
