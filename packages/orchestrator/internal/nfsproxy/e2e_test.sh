#!/usr/bin/env bash

set -e
set -x

if [[ -z "${NFS_HOST:-}" ]]; then
  echo "ERROR: NFS_HOST environment variable is required" >&2
  exit 1
fi

if [[ -z "${NFS_PORT:-}" ]]; then
  echo "ERROR: NFS_PORT environment variable is required" >&2
  exit 1
fi

if [[ -z "${NFS_PATH:-}" ]]; then
  echo "ERROR: NFS_PATH environment variable is required" >&2
  exit 1
fi

echo "installing nfs tools"
apt update --quiet
apt install nfs-common --yes --quiet

echo "connect to nfs server at ${NFS_HOST} as $(whoami)"

mkdir -p /mnt/shared/test-volume-1
mount -t nfs -v \
  -o vers=3,mountport=${NFS_PORT},mountproto=tcp,port=${NFS_PORT},proto=tcp,noacl,nolock \
  "${NFS_HOST}:${NFS_PATH}" \
  /mnt/shared/test-volume-1

echo "writing files to nfs"

echo "hello world" > /mnt/shared/test-volume-1/test.txt
cat /mnt/shared/test-volume-1/test.txt
