#!/usr/bin/env bash
# Start script for worker (client) nodes on AWS.
# Handles: disk setup, hugepages, EFS mount, Consul/Nomad client start.

set -euo pipefail

PS4='[\D{%Y-%m-%d %H:%M:%S}] '
set -x

exec > >(tee /var/log/user-data.log | logger -t user-data -s 2>/dev/console) 2>&1

# --- Disk Setup ---
# For .metal instances, NVMe instance store disks are available at /dev/nvme*n1
# Detect and set up cache disk
MOUNT_POINT="/orchestrator"

# Check for NVMe instance store devices (common on i3.metal, c5d.metal, etc.)
NVME_DISKS=($(ls /dev/nvme*n1 2>/dev/null | grep -v nvme0n1 || true))

if [[ $${#NVME_DISKS[@]} -gt 0 ]]; then
  if [[ $${#NVME_DISKS[@]} -gt 1 ]]; then
    # RAID 0 multiple NVMe disks
    DISK="/dev/md0"
    mdadm --create --verbose $DISK --raid-devices=$${#NVME_DISKS[@]} $${NVME_DISKS[@]} --level=0 || true
    mdadm --detail --scan --verbose | tee -a /etc/mdadm/mdadm.conf
  else
    DISK="$${NVME_DISKS[0]}"
  fi
else
  # Fall back to second EBS volume if no instance store
  DISK="/dev/xvdb"
  if [[ ! -b "$DISK" ]]; then
    DISK="/dev/nvme1n1"
  fi
fi

# Format and mount the cache disk
until mkfs.xfs -f -b size=4096 $DISK; do
  echo "failed to make file system, trying again..."
  sleep 1
done

mkdir -p $MOUNT_POINT
echo "$DISK    $MOUNT_POINT    xfs noatime 0 0" | tee -a /etc/fstab
mount "$MOUNT_POINT"

mkdir -p /orchestrator/sandbox
mkdir -p /orchestrator/template
mkdir -p /orchestrator/build

# Add swapfile
SWAPFILE="/swapfile"
fallocate -l 100G $SWAPFILE
chmod 600 $SWAPFILE
mkswap $SWAPFILE
swapon $SWAPFILE
echo "$SWAPFILE none swap sw 0 0" | tee -a /etc/fstab

sysctl vm.swappiness=10
sysctl vm.vfs_cache_pressure=50

# --- EFS Mount ---
%{ if USE_EFS_CACHE }
mkdir -p "${EFS_MOUNT_PATH}"
# EFS uses NFSv4.1
echo "${EFS_DNS_NAME}:/ ${EFS_MOUNT_PATH} nfs4 nfsvers=4.1,rsize=1048576,wsize=1048576,hard,timeo=600,retrans=2,noresvport 0 0" | tee -a /etc/fstab
mount "${EFS_MOUNT_PATH}"
mkdir -p "${EFS_MOUNT_PATH}/${EFS_MOUNT_SUBDIR}" && chmod +w "${EFS_MOUNT_PATH}/${EFS_MOUNT_SUBDIR}"
%{ endif }

# --- Tmpfs for snapshotting ---
mkdir -p /mnt/snapshot-cache
mount -t tmpfs -o size=65G tmpfs /mnt/snapshot-cache

# --- System tuning ---
ulimit -n 1048576
export GOMAXPROCS='nproc'

tee -a /etc/sysctl.conf <<EOF
net.core.somaxconn = 65535
net.core.netdev_max_backlog = 65535
net.ipv4.tcp_max_syn_backlog = 65535
vm.max_map_count=1048576
EOF
sysctl -p

# --- NBD setup ---
echo "Disabling inotify for NBD devices"
cat <<EOH > /etc/udev/rules.d/97-nbd-device.rules
ACTION=="add|change", KERNEL=="nbd*", OPTIONS:="nowatch"
EOH
udevadm control --reload-rules
udevadm trigger
modprobe nbd nbds_max=4096

mkdir -p /fc-vm

# --- Download setup scripts from S3 ---
aws s3 cp "s3://${SCRIPTS_BUCKET}/run-consul-${RUN_CONSUL_FILE_HASH}.sh" /opt/consul/bin/run-consul.sh --region "${AWS_REGION}"
aws s3 cp "s3://${SCRIPTS_BUCKET}/run-nomad-${RUN_NOMAD_FILE_HASH}.sh" /opt/nomad/bin/run-nomad.sh --region "${AWS_REGION}"
chmod +x /opt/consul/bin/run-consul.sh /opt/nomad/bin/run-nomad.sh

# --- Docker auth for ECR ---
IMDS_TOKEN=$(curl -s -X PUT "http://169.254.169.254/latest/api/token" -H "X-aws-ec2-metadata-token-ttl-seconds: 300")
AWS_ACCOUNT_ID=$(curl -s -H "X-aws-ec2-metadata-token: $IMDS_TOKEN" http://169.254.169.254/latest/dynamic/instance-identity/document | jq -r '.accountId')
ECR_PASSWORD=$(aws ecr get-login-password --region "${AWS_REGION}")

mkdir -p /root/docker
cat <<DOCKEREOF > /root/docker/config.json
{
    "auths": {
        "$${AWS_ACCOUNT_ID}.dkr.ecr.${AWS_REGION}.amazonaws.com": {
            "auth": "$(echo -n "AWS:$${ECR_PASSWORD}" | base64)"
        }
    }
}
DOCKEREOF

# --- DNS setup ---
mkdir -p /etc/systemd/resolved.conf.d/
cat <<EOF > /etc/systemd/resolved.conf.d/consul.conf
[Resolve]
DNS=127.0.0.1:8600
DNSSEC=false
EOF
sync

# --- Hugepages setup ---
echo "[Setting up huge pages]"
mkdir -p /mnt/hugepages
mount -t hugetlbfs none /mnt/hugepages

available_ram=$(grep MemTotal /proc/meminfo | awk '{print $2}')
available_ram=$(($available_ram / 1024))

min_normal_ram=$((4 * 1024))
min_normal_percentage_ram=$(($available_ram * 16 / 100))
max_normal_ram=$((42 * 1024))

max() { if (($1 > $2)); then echo "$1"; else echo "$2"; fi; }
min() { if (($1 < $2)); then echo "$1"; else echo "$2"; fi; }
ensure_even() { if (($1 % 2 == 0)); then echo "$1"; else echo $(($1 - 1)); fi; }
remove_decimal() { echo "$(echo $1 | sed 's/\..*//')"; }

reserved_normal_ram=$(max $min_normal_ram $min_normal_percentage_ram)
reserved_normal_ram=$(min $reserved_normal_ram $max_normal_ram)

hugepages_ram=$(($available_ram - $reserved_normal_ram))
hugepages_ram=$(remove_decimal $hugepages_ram)
hugepages_ram=$(ensure_even $hugepages_ram)

hugepage_size_in_mib=2
hugepages=$(($hugepages_ram / $hugepage_size_in_mib))

base_hugepages_percentage=${BASE_HUGEPAGES_PERCENTAGE}
base_hugepages=$(($hugepages * $base_hugepages_percentage / 100))
base_hugepages=$(remove_decimal $base_hugepages)
echo $base_hugepages > /proc/sys/vm/nr_hugepages

overcommitment_hugepages_percentage=$((100 - $base_hugepages_percentage))
overcommitment_hugepages=$(($hugepages * $overcommitment_hugepages_percentage / 100))
overcommitment_hugepages=$(remove_decimal $overcommitment_hugepages)
echo $overcommitment_hugepages > /proc/sys/vm/nr_overcommit_hugepages

# --- Start Consul ---
# Use Amazon-provided DNS as recursor (VPC DNS at .2 of VPC CIDR)
VPC_DNS="169.254.169.253"

/opt/consul/bin/run-consul.sh --client \
    --consul-token "${CONSUL_TOKEN}" \
    --cluster-tag-name "orch" \
    --enable-gossip-encryption \
    --gossip-encryption-key "${CONSUL_GOSSIP_ENCRYPTION_KEY}" \
    --dns-request-token "${CONSUL_DNS_REQUEST_TOKEN}" \
    --recursor "$VPC_DNS" &

echo "- Waiting for Consul DNS to start on port 8600..."
for i in {1..10}; do
  if nc -z 127.0.0.1 8600 2>/dev/null; then
    echo "- Consul DNS is ready (attempt $i/10)"
    break
  fi
  if [ $i -eq 10 ]; then
    echo "- ERROR: Consul DNS not responding after 10 seconds"
    exit 1
  fi
  sleep 1
done

systemctl restart systemd-resolved

echo "- Waiting for DNS resolution..."
for i in {1..10}; do
  if host google.com 2>/dev/null; then
    echo "- DNS resolving is ready (attempt $i/10)"
    break
  fi
  if [ $i -eq 10 ]; then
    echo "- ERROR: DNS not responding after 10 seconds"
    exit 1
  fi
  sleep 1
done
resolvectl flush-caches

# --- Start Nomad ---
/opt/nomad/bin/run-nomad.sh --client --consul-token "${CONSUL_TOKEN}" --node-pool "${NODE_POOL}" &

# --- SSH alias ---
echo '_sbx_ssh() {
  local address=$(dig @127.0.0.4 $1. A +short 2>/dev/null)
  ssh -o StrictHostKeyChecking=accept-new "root@$address"
}
alias sbx-ssh=_sbx_ssh' >> /etc/profile
