#!/usr/bin/env bash
# Cloud-Init for Redis primary or replica.
# 1:1 functional with managed-Redis (ElastiCache/Memorystore) — manual on Hetzner.

set -euo pipefail
PS4='[\D{%Y-%m-%d %H:%M:%S}] '
set -x
exec > >(tee /var/log/user-data.log | logger -t user-data -s 2>/dev/console) 2>&1

INSTANCE_ID=$(curl -sf http://169.254.169.254/hetzner/v1/metadata/instance-id 2>/dev/null || echo "$$")
sudo hostnamectl set-hostname "${HOSTNAME_SUFFIX}-$${INSTANCE_ID}"

# Wait for attached data-volume + mount.
for i in $(seq 1 30); do
  VOL_DEV=$(ls /dev/disk/by-id/scsi-0HC_Volume_* 2>/dev/null | head -1 || true)
  if [ -n "$VOL_DEV" ] && [ -b "$VOL_DEV" ]; then break; fi
  sleep 2
done
[ -n "$VOL_DEV" ] || { echo "FATAL: data volume never appeared"; exit 1; }

mkdir -p ${PERSIST_DIR}
if ! mountpoint -q ${PERSIST_DIR}; then
  mount "$VOL_DEV" ${PERSIST_DIR} || {
    mkfs.ext4 -F "$VOL_DEV"
    mount "$VOL_DEV" ${PERSIST_DIR}
  }
  echo "$VOL_DEV ${PERSIST_DIR} ext4 discard,defaults 0 2" | sudo tee -a /etc/fstab
fi

chown -R redis:redis ${PERSIST_DIR}

# Build redis.conf for role.
sudo tee /etc/redis/redis.conf >/dev/null <<EOF
bind 0.0.0.0
port ${REDIS_PORT}
requirepass ${REDIS_AUTH_TOKEN}
masterauth ${REDIS_AUTH_TOKEN}

# Persistence: AOF (always-on) + RDB snapshots
appendonly yes
appendfsync everysec
save 900 1
save 300 10
save 60 10000

dir ${PERSIST_DIR}
dbfilename dump.rdb
appendfilename "appendonly.aof"

# Tuning (1:1 ElastiCache defaults)
maxmemory-policy allkeys-lru
tcp-keepalive 60
timeout 0
EOF

# Replica-specific: replicaof primary
if [ "${REDIS_ROLE}" = "replica" ]; then
  sudo tee -a /etc/redis/redis.conf >/dev/null <<EOF

replicaof ${REDIS_PRIMARY_HOST} ${REDIS_PRIMARY_PORT}
replica-read-only yes
EOF
fi

sudo systemctl enable --now redis-server
sleep 2
sudo systemctl status redis-server --no-pager | head -10

echo "Redis ${REDIS_ROLE} bootstrap complete on ${HOSTNAME_SUFFIX}"
