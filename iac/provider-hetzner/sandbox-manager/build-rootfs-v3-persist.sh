#!/usr/bin/env bash
# Build rootfs v3 with SSH-server + sshd autostart for persistent VMs
set -e
SSH_DIR=${MAXICORE_SSH_DIR:-/etc/maxicore-sandbox/ssh}
mkdir -p $SSH_DIR
chmod 700 $SSH_DIR

[ -f $SSH_DIR/sandbox_id_ed25519 ] || ssh-keygen -t ed25519 -f $SSH_DIR/sandbox_id_ed25519 -N "" -C "maxicore-sandbox-manager"
PUBKEY=$(cat $SSH_DIR/sandbox_id_ed25519.pub)

ROOTFS_SRC=${MAXICORE_ROOTFS_SRC:-/opt/firecracker/rootfs/ubuntu-22.04.ext4}
ROOTFS_V3=${MAXICORE_ROOTFS_V3:-/opt/firecracker/rootfs/ubuntu-22.04-v3-persist.ext4}
cp -f $ROOTFS_SRC $ROOTFS_V3

MOUNT=$(mktemp -d)
mount -o loop $ROOTFS_V3 $MOUNT

mkdir -p $MOUNT/root/.ssh
echo "$PUBKEY" > $MOUNT/root/.ssh/authorized_keys
chmod 700 $MOUNT/root/.ssh
chmod 600 $MOUNT/root/.ssh/authorized_keys

chroot $MOUNT /usr/bin/ssh-keygen -A 2>/dev/null || true

mkdir -p $MOUNT/etc/ssh/sshd_config.d
cat > $MOUNT/etc/ssh/sshd_config.d/maxicore.conf <<SSHD
PermitRootLogin prohibit-password
PasswordAuthentication no
PubkeyAuthentication yes
ChallengeResponseAuthentication no
UsePAM no
X11Forwarding no
PrintMotd no
SSHD

cat > $MOUNT/maxicore-init.sh <<'INIT'
#!/bin/sh
mount -t proc proc /proc 2>/dev/null
mount -t sysfs sys /sys 2>/dev/null
mount -t devpts devpts /dev/pts 2>/dev/null
mount -t tmpfs tmpfs /run 2>/dev/null
mount -t tmpfs tmpfs /tmp 2>/dev/null
ip link set lo up
ip link set eth0 up 2>/dev/null
echo "nameserver 1.1.1.1" > /etc/resolv.conf
echo "nameserver 9.9.9.9" >> /etc/resolv.conf
hostname maxicore-vm-$(date +%s%N | tail -c 6)
mkdir -p /run/sshd
/usr/sbin/sshd -D &
echo "READY $(date -u +%H:%M:%S.%N)" > /tmp/maxicore-ready
exec sleep infinity
INIT
chmod +x $MOUNT/maxicore-init.sh

sync
umount $MOUNT
rmdir $MOUNT

echo "✅ Rootfs v3 (persistent SSH) ready at $ROOTFS_V3"
