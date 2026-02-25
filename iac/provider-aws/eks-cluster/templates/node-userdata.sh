#!/bin/bash
set -euo pipefail

# Format and mount cache disk
if [ -b "${CACHE_DISK_DEVICE}" ]; then
  if ! blkid "${CACHE_DISK_DEVICE}" &>/dev/null; then
    mkfs.ext4 -F "${CACHE_DISK_DEVICE}"
  fi
  mkdir -p "${CACHE_MOUNT_PATH}"
  mount "${CACHE_DISK_DEVICE}" "${CACHE_MOUNT_PATH}"
  echo "${CACHE_DISK_DEVICE} ${CACHE_MOUNT_PATH} ext4 defaults,nofail 0 2" >> /etc/fstab
fi

# Load NBD kernel module for Firecracker block device overlays
modprobe nbd max_part=0 nbds_max=4096
echo "nbd" > /etc/modules-load.d/nbd.conf
echo "options nbd max_part=0 nbds_max=4096" > /etc/modprobe.d/nbd.conf

# Configure hugepages for Firecracker VM memory
%{ if HUGEPAGES_PERCENTAGE > 0 ~}
TOTAL_MEM_KB=$(grep MemTotal /proc/meminfo | awk '{print $2}')
HUGEPAGE_SIZE_KB=2048
TARGET_HUGEPAGES=$(( TOTAL_MEM_KB * ${HUGEPAGES_PERCENTAGE} / 100 / HUGEPAGE_SIZE_KB ))
echo "$TARGET_HUGEPAGES" > /proc/sys/vm/nr_hugepages
echo "vm.nr_hugepages=$TARGET_HUGEPAGES" >> /etc/sysctl.conf
%{ endif ~}

# Mount EFS if configured
%{ if EFS_DNS_NAME != "" ~}
mkdir -p "${EFS_MOUNT_PATH}"
mount -t nfs4 -o nfsvers=4.1,rsize=1048576,wsize=1048576,hard,timeo=600,retrans=2 \
  "${EFS_DNS_NAME}:/" "${EFS_MOUNT_PATH}"
echo "${EFS_DNS_NAME}:/ ${EFS_MOUNT_PATH} nfs4 nfsvers=4.1,rsize=1048576,wsize=1048576,hard,timeo=600,retrans=2,_netdev 0 0" >> /etc/fstab
%{ endif ~}
