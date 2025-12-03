#!/usr/bin/env bash
set -euo pipefail
if exportfs -v 2>/dev/null | awk '{print $1}' | grep -qx '/e2b-share' && rpcinfo -p 2>/dev/null | grep -q nfs; then
  exit 0
fi
mkdir -p /e2b-share/templates
chmod 1777 /e2b-share/templates

mkdir -p /e2b-share/build-cache
chmod 1777 /e2b-share/build-cache
if command -v apt-get >/dev/null 2>&1; then
  apt-get update -y
  apt-get install -y nfs-kernel-server nfs-common rpcbind
  systemctl enable --now rpcbind
  systemctl enable --now nfs-kernel-server
elif command -v yum >/dev/null 2>&1; then
  yum install -y nfs-utils rpcbind
  systemctl enable --now rpcbind
  systemctl enable --now nfs-server
else
  echo "No known package manager found (apt-get/yum)" >&2
  exit 1
fi

printf '/e2b-share *(rw,sync,insecure,no_root_squash,no_subtree_check,fsid=0)\n' > /etc/exports
exportfs -ra || { echo "exportfs refresh failed" >&2; exit 1; }

# Validate NFSv4 service without relying on v3 mountd
exportfs -v || { echo "Exportfs validation failed" >&2; exit 1; }
rpcinfo -p | grep -q nfs || { echo "nfsd not registered" >&2; exit 1; }
