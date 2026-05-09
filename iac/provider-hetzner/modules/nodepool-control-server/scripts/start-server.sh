#!/usr/bin/env bash
# Cloud-Init for Nomad/Consul SERVER nodes (cluster control plane).
# 1:1 with provider-aws/start-server.sh, Hetzner-native bootstrap.

set -euo pipefail
PS4='[\D{%Y-%m-%d %H:%M:%S}] '
set -x
exec > >(tee /var/log/user-data.log | logger -t user-data -s 2>/dev/console) 2>&1

ulimit -n 1048576
export GOMAXPROCS=$(nproc)

INSTANCE_ID=$(curl -sf http://169.254.169.254/hetzner/v1/metadata/instance-id 2>/dev/null || echo "$$")
sudo hostnamectl set-hostname "${HOSTNAME_SUFFIX}-$${INSTANCE_ID}"

# MinIO Client + bootstrap secrets
if ! command -v mc >/dev/null 2>&1; then
  curl -sfL https://dl.min.io/client/mc/release/linux-amd64/mc -o /usr/local/bin/mc
  chmod +x /usr/local/bin/mc
fi
mc alias set hetzner "${OBJECT_STORE_URL}" "${OBJECT_STORE_ACCESS_KEY}" "${OBJECT_STORE_SECRET_KEY}"

mkdir -p /opt/cluster-bootstrap
mc cp "hetzner/${SCRIPTS_BUCKET}/bootstrap/secrets.json" /opt/cluster-bootstrap/secrets.json

# Run consul + nomad in SERVER mode (Raft leader/voter, not worker).
[ -x /opt/consul/bin/run-consul.sh ] && /opt/consul/bin/run-consul.sh \
  --server \
  --num-servers "${NUM_SERVERS}" \
  --cluster-tag-name "${CLUSTER_TAG_NAME}" \
  --cluster-tag-value "${CLUSTER_TAG_VALUE}" \
  --acl-token "${CONSUL_TOKEN}" \
  --gossip-encryption-key "${CONSUL_GOSSIP_ENCRYPTION_KEY}"

[ -x /opt/nomad/bin/run-nomad.sh ] && /opt/nomad/bin/run-nomad.sh \
  --server \
  --num-servers "${NUM_SERVERS}" \
  --cluster-tag-name "${CLUSTER_TAG_NAME}" \
  --cluster-tag-value "${CLUSTER_TAG_VALUE}" \
  --acl-token "${NOMAD_TOKEN}"

echo "Control-server bootstrap complete: $${HOSTNAME_SUFFIX}-$${INSTANCE_ID}"
