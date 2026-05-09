#!/usr/bin/env bash
# Cloud-Init for ClickHouse nodes. 1:1 with provider-aws/start-clickhouse.sh
# but uses Hetzner Object Storage for cold-tier backup.

set -euo pipefail
PS4='[\D{%Y-%m-%d %H:%M:%S}] '
set -x
exec > >(tee /var/log/user-data.log | logger -t user-data -s 2>/dev/console) 2>&1

ulimit -n 1048576
export GOMAXPROCS=$(nproc)

# Hostname
INSTANCE_ID=$(curl -sf http://169.254.169.254/hetzner/v1/metadata/instance-id 2>/dev/null || echo "$$")
sudo hostnamectl set-hostname "${HOSTNAME_SUFFIX}-$${INSTANCE_ID}"

# Wait for the attached Cloud Volume to appear, then mount it.
# Hetzner attaches volumes as /dev/disk/by-id/scsi-0HC_Volume_<ID>.
for i in $(seq 1 30); do
  VOL_DEV=$(ls ${DATA_VOLUME_DEVICE} 2>/dev/null | head -1 || true)
  if [ -n "$VOL_DEV" ] && [ -b "$VOL_DEV" ]; then break; fi
  sleep 2
done
if [ -z "$VOL_DEV" ]; then
  echo "FATAL: data volume never appeared"; exit 1
fi

mkdir -p /var/lib/clickhouse
if ! mountpoint -q /var/lib/clickhouse; then
  mount "$VOL_DEV" /var/lib/clickhouse || {
    mkfs.ext4 -F "$VOL_DEV"
    mount "$VOL_DEV" /var/lib/clickhouse
  }
  echo "$VOL_DEV /var/lib/clickhouse ext4 discard,defaults 0 2" | sudo tee -a /etc/fstab
fi

# Install MinIO Client for Object Storage backup access.
if ! command -v mc >/dev/null 2>&1; then
  curl -sfL https://dl.min.io/client/mc/release/linux-amd64/mc -o /usr/local/bin/mc
  chmod +x /usr/local/bin/mc
fi
mc alias set hetzner "${OBJECT_STORE_URL}" "${OBJECT_STORE_ACCESS_KEY}" "${OBJECT_STORE_SECRET_KEY}"

# Pull cluster-bootstrap secrets.
mkdir -p /opt/cluster-bootstrap
mc cp "hetzner/${SCRIPTS_BUCKET}/bootstrap/secrets.json" /opt/cluster-bootstrap/secrets.json

# ClickHouse storage policy: write hot tier to /var/lib/clickhouse, cold tier to Object Storage.
sudo tee /etc/clickhouse-server/config.d/storage.xml >/dev/null <<EOF
<clickhouse>
  <storage_configuration>
    <disks>
      <hot><path>/var/lib/clickhouse/</path></hot>
      <cold>
        <type>s3</type>
        <endpoint>${OBJECT_STORE_URL}/${CLICKHOUSE_BACKUPS_BUCKET}/clickhouse-cold/</endpoint>
        <access_key_id>${OBJECT_STORE_ACCESS_KEY}</access_key_id>
        <secret_access_key>${OBJECT_STORE_SECRET_KEY}</secret_access_key>
      </cold>
    </disks>
  </storage_configuration>
</clickhouse>
EOF

# Start consul + nomad agents (scripts come from snapshot, NX.2.5).
[ -x /opt/consul/bin/run-consul.sh ] && /opt/consul/bin/run-consul.sh \
  --node-pool "${NODE_POOL}" \
  --cluster-tag-name "${CLUSTER_TAG_NAME}" \
  --cluster-tag-value "${CLUSTER_TAG_VALUE}" \
  --acl-token "${CONSUL_TOKEN}" \
  --gossip-encryption-key "${CONSUL_GOSSIP_ENCRYPTION_KEY}"

[ -x /opt/nomad/bin/run-nomad.sh ] && /opt/nomad/bin/run-nomad.sh \
  --node-pool "${NODE_POOL}" \
  --cluster-tag-name "${CLUSTER_TAG_NAME}" \
  --cluster-tag-value "${CLUSTER_TAG_VALUE}" \
  --acl-token "${NOMAD_TOKEN}"

echo "ClickHouse node bootstrap complete"
