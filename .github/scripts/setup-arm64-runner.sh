#!/usr/bin/env bash
#
# Setup script for an ARM64 self-hosted GitHub Actions runner.
# Run this on a fresh ARM64 Ubuntu 22.04/24.04 machine with KVM support.
#
# Prerequisites:
#   - ARM64 Linux host (Graviton, Ampere, etc.)
#   - KVM enabled (/dev/kvm accessible)
#   - At least 8GB RAM (for hugepage allocation)
#   - Root access
#
# Usage:
#   sudo ./setup-arm64-runner.sh
#
# After running this script, register the machine as a GitHub Actions
# self-hosted runner with the label: infra-tests-arm64
#   https://github.com/e2b-dev/infra/settings/actions/runners/new

set -euo pipefail

PS4='[\D{%Y-%m-%d %H:%M:%S}] '
set -x

if [ "$(id -u)" -ne 0 ]; then
    echo "ERROR: This script must be run as root" >&2
    exit 1
fi

ARCH=$(dpkg --print-architecture)
if [ "$ARCH" != "arm64" ]; then
    echo "ERROR: This script is for ARM64 hosts (detected: $ARCH)" >&2
    exit 1
fi

echo "=== Setting up ARM64 GitHub Actions runner ==="

# KVM check
if [ ! -e /dev/kvm ]; then
    echo "ERROR: /dev/kvm not found. KVM support is required." >&2
    exit 1
fi

# Install base dependencies
apt-get update
apt-get install -y --no-install-recommends \
    build-essential \
    curl \
    git \
    jq \
    nbd-client \
    nbd-server

# Enable unprivileged userfaultfd
echo 1 > /proc/sys/vm/unprivileged_userfaultfd

# Hugepages
mkdir -p /mnt/hugepages
mount -t hugetlbfs none /mnt/hugepages 2>/dev/null || true
echo 2000 > /proc/sys/vm/nr_hugepages

grep -qF 'hugetlbfs /mnt/hugepages' /etc/fstab || \
    echo "hugetlbfs /mnt/hugepages hugetlbfs defaults 0 0" >> /etc/fstab

# Sysctl — write once (idempotent)
cat <<'EOF' > /etc/sysctl.d/99-e2b.conf
vm.unprivileged_userfaultfd=1
vm.nr_hugepages=2000
net.core.somaxconn=65535
net.core.netdev_max_backlog=65535
net.ipv4.tcp_max_syn_backlog=65535
vm.max_map_count=1048576
EOF
sysctl --system

# NBD
modprobe nbd nbds_max=256
echo "nbd" > /etc/modules-load.d/e2b.conf
echo "options nbd nbds_max=256" > /etc/modprobe.d/e2b-nbd.conf

# Disable inotify for NBD devices
cat <<'EOF' > /etc/udev/rules.d/97-nbd-device.rules
ACTION=="add|change", KERNEL=="nbd*", OPTIONS:="nowatch"
EOF
udevadm control --reload-rules
udevadm trigger

# File descriptor limits
cat <<'EOF' > /etc/security/limits.d/99-e2b.conf
* soft nofile 1048576
* hard nofile 1048576
EOF

echo ""
echo "=== ARM64 runner setup complete ==="
echo ""
echo "Verify:"
echo "  uname -m              → aarch64"
echo "  ls /dev/kvm            → exists"
echo "  cat /proc/meminfo | grep HugePages_Total"
echo "  lsmod | grep nbd"
echo ""
echo "Next: register this machine as a GitHub Actions self-hosted runner"
echo "  Label: infra-tests-arm64"
echo "  https://github.com/e2b-dev/infra/settings/actions/runners/new"
