#!/usr/bin/env bash
set -euo pipefail

%{ if use_nfs_share_storage }
is_mounted() { grep -qsE '[[:space:]]/e2b-share[[:space:]]' /proc/mounts; }
if is_mounted; then
  echo "/e2b-share already mounted,skipping."
else
  if ! command -v mount.nfs >/dev/null 2>&1; then
    if command -v apt-get >/dev/null 2>&1; then
      apt-get update -y
      apt-get install -y nfs-common rpcbind
      systemctl enable --now rpcbind || true
    elif command -v yum >/dev/null 2>&1; then
      yum install -y nfs-utils rpcbind
      systemctl enable --now rpcbind || true
    fi
  fi
  LOCAL_IP=$(hostname -I 2>/dev/null | awk '{print $1}')
  if [ "${nfs_server_ip}" != "$LOCAL_IP" ]; then
    for i in $(seq 1 10); do
      if mount -t nfs -o vers=4,hard,rsize=1048576,wsize=1048576,timeo=600,retrans=2 "${nfs_server_ip}:/" /e2b-share; then
        break
      fi
      if mount -t nfs4 -o hard,timeo=600,retrans=2 "${nfs_server_ip}:/" /e2b-share; then
        break
      fi
      sleep 2
    done
  fi
  is_mounted || { echo "NFS mount failed" >&2; exit 1; }
  mkdir -p /e2b-share/templates || true
  touch /e2b-share/templates/.nfs-write-test.$$ || { echo "NFS write test failed" >&2; exit 1; }
  rm -f /e2b-share/templates/.nfs-write-test.$$ || true
fi
%{ endif }

modprobe nbd nbds_max=4096 max_part=16 || true
for i in $(seq 0 4095); do
  if [ -e /sys/block/nbd"$i"/pid ]; then nbd-client -d /dev/nbd"$i" || true; fi
done
sysctl -w vm.nr_hugepages=2048
chmod +x local/orchestrator
exec local/orchestrator
