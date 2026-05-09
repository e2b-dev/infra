#!/usr/bin/env bash
# Cloud-Init for Firecracker-Host (Client) nodes. KRITISCH für Manus-Parity.
# Configures HugePages, KVM, Firecracker pre-baked binaries, Warmpool snapshots.

set -euo pipefail
PS4='[\D{%Y-%m-%d %H:%M:%S}] '
set -x
exec > >(tee /var/log/user-data.log | logger -t user-data -s 2>/dev/console) 2>&1

ulimit -n 1048576
export GOMAXPROCS=$(nproc)

INSTANCE_ID=$(curl -sf http://169.254.169.254/hetzner/v1/metadata/instance-id 2>/dev/null || echo "$$")
sudo hostnamectl set-hostname "${HOSTNAME_SUFFIX}-$${INSTANCE_ID}"

# ───────────────────────── KVM check ─────────────────────────
# Hetzner Cloud Server (CCX = Dedicated CPUs) supports nested virt for KVM.
if [ ! -e /dev/kvm ]; then
  echo "FATAL: /dev/kvm not present. CCX or Robot bare-metal required for Firecracker."
  exit 1
fi
chmod 666 /dev/kvm

# ───────────────────────── HugePages (Manus 1:1 Pattern) ─────────────────────────
TOTAL_RAM_KB=$(awk '/MemTotal/ {print $2}' /proc/meminfo)
HUGEPAGES_KB=$((TOTAL_RAM_KB * ${BASE_HUGEPAGES_PERCENTAGE} / 100))
HUGEPAGES_2M=$((HUGEPAGES_KB / 2048))

echo "Allocating $${HUGEPAGES_2M} 2MB HugePages (~$${BASE_HUGEPAGES_PERCENTAGE}% of RAM)"
echo "$${HUGEPAGES_2M}" | sudo tee /proc/sys/vm/nr_hugepages
sudo tee -a /etc/sysctl.conf <<EOF
vm.nr_hugepages = $${HUGEPAGES_2M}
vm.swappiness = 0
EOF

# Make HugePages mount permanent (Firecracker requires hugetlbfs).
sudo mkdir -p /dev/hugepages
if ! mountpoint -q /dev/hugepages; then
  sudo mount -t hugetlbfs none /dev/hugepages
fi
echo "hugetlbfs /dev/hugepages hugetlbfs defaults 0 0" | sudo tee -a /etc/fstab

# ───────────────────────── MinIO Client + Bootstrap ─────────────────────────
if ! command -v mc >/dev/null 2>&1; then
  curl -sfL https://dl.min.io/client/mc/release/linux-amd64/mc -o /usr/local/bin/mc
  chmod +x /usr/local/bin/mc
fi
mc alias set hetzner "${OBJECT_STORE_URL}" "${OBJECT_STORE_ACCESS_KEY}" "${OBJECT_STORE_SECRET_KEY}"

mkdir -p /opt/cluster-bootstrap
mc cp "hetzner/${SCRIPTS_BUCKET}/bootstrap/secrets.json" /opt/cluster-bootstrap/secrets.json

# ───────────────────────── Firecracker Assets ─────────────────────────
# Pull pre-baked Firecracker binary, kernels, and rootfs templates from Object Storage.
# These are produced by NX.3 (vm-rootfs) and NX.4 (vm-kernel) pipelines.
mkdir -p /opt/firecracker/{bin,kernels,rootfs,busybox,env-pipeline}

# Latest Firecracker binary (versioned).
mc mirror "hetzner/${FC_VERSIONS_BUCKET}/latest/" /opt/firecracker/bin/ || true
chmod +x /opt/firecracker/bin/firecracker /opt/firecracker/bin/jailer 2>/dev/null || true

# Kernels (versioned).
mc mirror "hetzner/${FC_KERNELS_BUCKET}/" /opt/firecracker/kernels/ || true

# Env-pipeline (template-build artifacts).
mc mirror "hetzner/${FC_ENV_PIPELINE_BUCKET}/" /opt/firecracker/env-pipeline/ || true

# Busybox-rootfs (minimal sandbox base).
mc mirror "hetzner/${FC_BUSYBOX_BUCKET}/" /opt/firecracker/busybox/ || true

# ───────────────────────── Networking for Firecracker ─────────────────────────
# Firecracker uses tap-devices + bridge. Manus pattern: 169.254.0.21/30 link-local
# inside sandbox, NAT through host. Configured by orchestrator at sandbox-create time.
sudo modprobe tun || true
echo "tun" | sudo tee -a /etc/modules-load.d/tun.conf

# IP forwarding for sandbox NAT (Manus 1:1).
sudo tee -a /etc/sysctl.conf <<'EOF'
net.ipv4.ip_forward = 1
net.ipv4.conf.all.rp_filter = 0
EOF
sudo sysctl -p

# ───────────────────────── Consul + Nomad with node-labels ─────────────────────────
NODE_LABELS_ARG=""
if [ -n "${NODE_LABELS}" ]; then
  NODE_LABELS_ARG="--node-labels ${NODE_LABELS}"
fi

[ -x /opt/consul/bin/run-consul.sh ] && /opt/consul/bin/run-consul.sh \
  --node-pool "${NODE_POOL}" \
  --cluster-tag-name "${CLUSTER_TAG_NAME}" \
  --cluster-tag-value "${CLUSTER_TAG_VALUE}" \
  --acl-token "${CONSUL_TOKEN}" \
  --gossip-encryption-key "${CONSUL_GOSSIP_ENCRYPTION_KEY}" \
  --node-meta firecracker_host=true

[ -x /opt/nomad/bin/run-nomad.sh ] && /opt/nomad/bin/run-nomad.sh \
  --node-pool "${NODE_POOL}" \
  --cluster-tag-name "${CLUSTER_TAG_NAME}" \
  --cluster-tag-value "${CLUSTER_TAG_VALUE}" \
  --acl-token "${NOMAD_TOKEN}" \
  $${NODE_LABELS_ARG}

echo "Firecracker-Host bootstrap complete: $${HOSTNAME_SUFFIX}-$${INSTANCE_ID}"
