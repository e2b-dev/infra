#!/usr/bin/env bash
#
# Cloud-Init bootstrap script for nodepool-api Hetzner Cloud Servers.
# 1:1 with provider-aws/modules/nodepool-api/scripts/start-api.sh but
# uses Hetzner Object Storage (S3-compat) instead of AWS S3, and skips
# AWS-specific things (IMDSv2, ECR, etc.).
#
# Variables are interpolated by Terraform via templatefile().

set -euo pipefail

# Set timestamp format for trace logs
PS4='[\D{%Y-%m-%d %H:%M:%S}] '
set -x

# Send all output to user-data.log + syslog + console (Hetzner-native logging).
exec > >(tee /var/log/user-data.log | logger -t user-data -s 2>/dev/console) 2>&1

ulimit -n 1048576
export GOMAXPROCS=$(nproc)

# Kernel tuning for high-concurrency API servers (1:1 with provider-aws).
sudo tee -a /etc/sysctl.conf <<'EOF'
net.core.somaxconn = 65535
net.core.netdev_max_backlog = 65535
net.ipv4.tcp_max_syn_backlog = 65535
EOF
sudo sysctl -p

# Set hostname for cluster identification.
HOSTNAME_PREFIX="${HOSTNAME_SUFFIX}"
INSTANCE_ID=$(curl -sf http://169.254.169.254/hetzner/v1/metadata/instance-id 2>/dev/null || echo "$$")
sudo hostnamectl set-hostname "$${HOSTNAME_PREFIX}-$${INSTANCE_ID}"

# Install s3cmd / mc (MinIO Client) for Hetzner Object Storage access.
# These are pre-baked into the Packer-Snapshot in NX.2.6; if not present,
# install on the fly.
if ! command -v mc >/dev/null 2>&1; then
  curl -sfL https://dl.min.io/client/mc/release/linux-amd64/mc -o /usr/local/bin/mc
  chmod +x /usr/local/bin/mc
fi

# Configure mc alias for Hetzner Object Storage.
mc alias set hetzner "${OBJECT_STORE_URL}" "${OBJECT_STORE_ACCESS_KEY}" "${OBJECT_STORE_SECRET_KEY}"

# Pull cluster-bootstrap secrets from Object Storage.
mkdir -p /opt/cluster-bootstrap
mc cp "hetzner/${SCRIPTS_BUCKET}/bootstrap/secrets.json" /opt/cluster-bootstrap/secrets.json

# Run consul + nomad bootstrap scripts (baked into snapshot by NX.2.6).
# The actual run-consul.sh / run-nomad.sh are provided by NX.2.5 nomad-cluster.
if [ -x /opt/consul/bin/run-consul.sh ]; then
  /opt/consul/bin/run-consul.sh \
    --node-pool "${NODE_POOL}" \
    --cluster-tag-name "${CLUSTER_TAG_NAME}" \
    --cluster-tag-value "${CLUSTER_TAG_VALUE}" \
    --acl-token "${CONSUL_TOKEN}" \
    --gossip-encryption-key "${CONSUL_GOSSIP_ENCRYPTION_KEY}"
fi

if [ -x /opt/nomad/bin/run-nomad.sh ]; then
  /opt/nomad/bin/run-nomad.sh \
    --node-pool "${NODE_POOL}" \
    --cluster-tag-name "${CLUSTER_TAG_NAME}" \
    --cluster-tag-value "${CLUSTER_TAG_VALUE}" \
    --acl-token "${NOMAD_TOKEN}"
fi

echo "API node bootstrap complete: $${HOSTNAME_PREFIX}-$${INSTANCE_ID}"
