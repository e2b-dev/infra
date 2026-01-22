#!/usr/bin/env bash

# This script is meant to be run in the User Data of each EC2 Instance while it's booting. The script uses the
# run-nomad and run-consul scripts to configure and start Nomad and Consul in client mode. Note that this script
# assumes it's running in an AMI built from the Packer template in examples/nomad-consul-ami/nomad-consul.json.

set -euo pipefail

# Set timestamp format
PS4='[\D{%Y-%m-%d %H:%M:%S}] '
# Enable command tracing
set -x

# Send the log output from this script to user-data.log, syslog, and the console
# Inspired by https://alestic.com/2010/12/ec2-user-data-output/
exec > >(tee /var/log/user-data.log | logger -t user-data -s 2>/dev/console) 2>&1

%{ if LOCAL_SSD == "true" }
  # Add cache disk for orchestrator and swapfile
  for i in {0..${ CACHE_DISK_COUNT - 1 }}; do
    dev_path="/dev/disk/by-id/google-local-nvme-ssd-$i"
    echo "partitioning drive #$i"
    parted --script $dev_path \
        mklabel gpt \
        mkpart primary 0% 100% \
        set 1 raid on
  done

  %{ if CACHE_DISK_COUNT > 1 }
    DISK="/dev/md0"

    echo "creating the array"
    until mdadm --create --verbose \
      $DISK \
      --raid-devices=${ CACHE_DISK_COUNT } \
      %{ for i in range(CACHE_DISK_COUNT) ~}/dev/disk/by-id/google-local-nvme-ssd-${ i }-part1 %{ endfor }\
      --level=0; do
        echo "failed to create array, trying again ... "
        sleep 1
    done

    echo "persisting array configuration"
    mdadm --detail --scan --verbose | tee -a /etc/mdadm/mdadm.conf
  %{ else }
    DISK="/dev/disk/by-id/google-local-nvme-ssd-0-part1"
  %{ endif }
%{ else }
  # Add cache disk for orchestrator and swapfile
  # TODO: Parametrize this
  DISK="/dev/disk/by-id/google-persistent-disk-1"
%{ endif }

MOUNT_POINT="/orchestrator"

# Step 1: Format the disk with XFS and 65K block size
until mkfs.xfs -f -b size=4096 $DISK; do
  echo "failed to make file system, trying again ... "
  sleep 1
done

# Step 2: Create the mount point
mkdir -p $MOUNT_POINT

# Step 3: Mount the disk with
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

# Make swapfile persistent
echo "$SWAPFILE none swap sw 0 0" | tee -a /etc/fstab

# Set swap settings
sysctl vm.swappiness=10
sysctl vm.vfs_cache_pressure=50

# TODO: Optimize the mount more according to https://cloud.google.com/filestore/docs/mounting-fileshares
%{ if USE_FILESTORE_CACHE }
# Configure NFS read ahead
cat <<'EOH' >/etc/udev/rules.d/99-nfs.rules
# set read_ahead_kb to 4096 (chunk size), from https://archive.is/3vAU7 (aka: https://support.vastdata.com/s/document-item?bundleId=z-kb-articles-publications-prod&topicId=6147145742.html&_LANG=enus)

SUBSYSTEM=="bdi", ACTION=="add", PROGRAM="/bin/awk -v bdi=$kernel 'BEGIN{ret=1} {if ($4 == bdi) {ret=0}} END{exit ret}' /proc/fs/nfsfs/volumes", ATTR{read_ahead_kb}="4096"
EOH
udevadm control --reload

# Mount NFS
mkdir -p "${NFS_MOUNT_PATH}"
echo "${NFS_IP_ADDRESS}:/store ${NFS_MOUNT_PATH} nfs ${NFS_MOUNT_OPTS} 0 0" | tee -a /etc/fstab
mount "${NFS_MOUNT_PATH}"
mkdir -p "${NFS_MOUNT_PATH}/${NFS_MOUNT_SUBDIR}" && chmod +w "${NFS_MOUNT_PATH}/${NFS_MOUNT_SUBDIR}"
%{ endif }

# Add tmpfs for snapshotting
# TODO: Parametrize this
mkdir -p /mnt/snapshot-cache
mount -t tmpfs -o size=65G tmpfs /mnt/snapshot-cache

ulimit -n 1048576
export GOMAXPROCS='nproc'

tee -a /etc/sysctl.conf <<EOF
# Increase the maximum number of socket connections
net.core.somaxconn = 65535

# Increase the maximum number of backlogged connections
net.core.netdev_max_backlog = 65535

# Increase maximum number of TCP sockets
net.ipv4.tcp_max_syn_backlog = 65535

# Increase the maximum number of memory map areas
vm.max_map_count=1048576

EOF
sysctl -p

echo "Disabling inotify for NBD devices"
# https://lore.kernel.org/lkml/20220422054224.19527-1-matthew.ruffell@canonical.com/
cat <<EOH >/etc/udev/rules.d/97-nbd-device.rules
# Disable inotify watching of change events for NBD devices
ACTION=="add|change", KERNEL=="nbd*", OPTIONS:="nowatch"
EOH

udevadm control --reload-rules
udevadm trigger

# Load the nbd module with 4096 devices
modprobe nbd nbds_max=4096

# Create the directory for the fc mounts
mkdir -p /fc-vm

# Create the config file for gcsfuse
fuse_cache="/fuse/cache"
mkdir -p $fuse_cache

fuse_config="/fuse/config.yaml"

cat >$fuse_config <<EOF
file-cache:
  max-size-mb: -1
  cache-file-for-range-read: false

metadata-cache:
  ttl-secs: -1

cache-dir: $fuse_cache
EOF

# Mount envd buckets
envd_dir="/fc-envd"
mkdir -p $envd_dir
gcsfuse -o=allow_other,ro --file-mode 755 --implicit-dirs "${FC_ENV_PIPELINE_BUCKET_NAME}" $envd_dir

# Mount kernels
kernels_dir="/fc-kernels"
mkdir -p $kernels_dir
gcsfuse -o=allow_other,ro --file-mode 755 --config-file $fuse_config --implicit-dirs "${FC_KERNELS_BUCKET_NAME}" $kernels_dir

# Mount FC versions
fc_versions_dir="/fc-versions"
mkdir -p $fc_versions_dir
gcsfuse -o=allow_other,ro --file-mode 755 --config-file $fuse_config --implicit-dirs "${FC_VERSIONS_BUCKET_NAME}" $fc_versions_dir

# These variables are passed in via Terraform template interpolation
gsutil cp "gs://${SCRIPTS_BUCKET}/run-consul-${RUN_CONSUL_FILE_HASH}.sh" /opt/consul/bin/run-consul.sh
gsutil cp "gs://${SCRIPTS_BUCKET}/run-nomad-${RUN_NOMAD_FILE_HASH}.sh" /opt/nomad/bin/run-nomad.sh

chmod +x /opt/consul/bin/run-consul.sh /opt/nomad/bin/run-nomad.sh

mkdir -p /root/docker
touch /root/docker/config.json
cat <<EOF >/root/docker/config.json
{
    "auths": {
        "${GCP_REGION}-docker.pkg.dev": {
            "username": "_json_key_base64",
            "password": "${GOOGLE_SERVICE_ACCOUNT_KEY}",
            "server_address": "https://${GCP_REGION}-docker.pkg.dev"
        }
    }
}
EOF

mkdir -p /etc/systemd/resolved.conf.d/
touch /etc/systemd/resolved.conf.d/consul.conf
cat <<EOF >/etc/systemd/resolved.conf.d/consul.conf
[Resolve]
DNS=127.0.0.1:8600
DNSSEC=false
EOF
sync  # Ensure file is written to disk

# Remove GCE's DNS config to prevent it from competing with Consul DNS (GCP-specific fix)
# We don't need routing domains since Consul handles ALL DNS:
#   - .consul queries: served directly by Consul
#   - other queries: forwarded to GCE DNS via Consul's recursor config
if [ -f /etc/systemd/resolved.conf.d/gce-resolved.conf ]; then
  mv /etc/systemd/resolved.conf.d/gce-resolved.conf /etc/systemd/resolved.conf.d/gce-resolved.conf.disabled
fi

# Set up huge pages
# We are not enabling Transparent Huge Pages for now, as they are not swappable and may result in slowdowns + we are not using swap right now.
# The THP are by default set to madvise
# We are allocating the hugepages at the start when the memory is not fragmented yet
echo "[Setting up huge pages]"
mkdir -p /mnt/hugepages
mount -t hugetlbfs none /mnt/hugepages
# Increase proactive compaction to reduce memory fragmentation for using overcomitted huge pages

available_ram=$(grep MemTotal /proc/meminfo | awk '{print $2}') # in KiB
available_ram=$(($available_ram / 1024))                        # in MiB
echo "- Total memory: $available_ram MiB"

min_normal_ram=$((4 * 1024))                             # 4 GiB
min_normal_percentage_ram=$(($available_ram * 16 / 100)) # 16% of the total memory
max_normal_ram=$((42 * 1024))                            # 42 GiB

max() {
    if (($1 > $2)); then
        echo "$1"
    else
        echo "$2"
    fi
}

min() {
    if (($1 < $2)); then
        echo "$1"
    else
        echo "$2"
    fi
}

ensure_even() {
    if (($1 % 2 == 0)); then
        echo "$1"
    else
        echo $(($1 - 1))
    fi
}

remove_decimal() {
    echo "$(echo $1 | sed 's/\..*//')"
}

reserved_normal_ram=$(max $min_normal_ram $min_normal_percentage_ram)
reserved_normal_ram=$(min $reserved_normal_ram $max_normal_ram)
echo "- Reserved RAM: $reserved_normal_ram MiB"

# The huge pages RAM should still be usable for normal pages in most cases.
hugepages_ram=$(($available_ram - $reserved_normal_ram))
hugepages_ram=$(remove_decimal $hugepages_ram)
hugepages_ram=$(ensure_even $hugepages_ram)
echo "- RAM for hugepages: $hugepages_ram MiB"

hugepage_size_in_mib=2
echo "- Huge page size: $hugepage_size_in_mib MiB"
hugepages=$(($hugepages_ram / $hugepage_size_in_mib))

# This percentage will be permanently allocated for huge pages and in monitoring it will be shown as used.
base_hugepages_percentage=${BASE_HUGEPAGES_PERCENTAGE}
base_hugepages=$(($hugepages * $base_hugepages_percentage / 100))
base_hugepages=$(remove_decimal $base_hugepages)
echo "- Allocating $base_hugepages huge pages ($base_hugepages_percentage%) for base usage"
echo $base_hugepages >/proc/sys/vm/nr_hugepages

overcommitment_hugepages_percentage=$((100 - $base_hugepages_percentage))
overcommitment_hugepages=$(($hugepages * $overcommitment_hugepages_percentage / 100))
overcommitment_hugepages=$(remove_decimal $overcommitment_hugepages)
echo "- Allocating $overcommitment_hugepages huge pages ($overcommitment_hugepages_percentage%) for overcommitment"
echo $overcommitment_hugepages >/proc/sys/vm/nr_overcommit_hugepages

# Get GCE DNS server dynamically from metadata for Consul recursors
# This ensures we can resolve internet domains through Consul
GCE_DNS=$(curl -s -H 'Metadata-Flavor: Google' http://metadata.google.internal/computeMetadata/v1/instance/network-interfaces/0/dns-servers || echo "169.254.169.254")

# Start Consul first (in background) with GCE DNS as recursor
# This allows Consul to handle both .consul queries AND forward internet queries
# These variables are passed in via Terraform template interpolation
/opt/consul/bin/run-consul.sh --client \
    --consul-token "${CONSUL_TOKEN}" \
    --cluster-tag-name "${CLUSTER_TAG_NAME}" \
    --enable-gossip-encryption \
    --gossip-encryption-key "${CONSUL_GOSSIP_ENCRYPTION_KEY}" \
    --dns-request-token "${CONSUL_DNS_REQUEST_TOKEN}" \
    --recursor "$${GCE_DNS}" &

# Give Consul a moment to start its DNS server on port 8600
echo "- Waiting for Consul DNS to start on port 8600..."
for i in {1..10}; do
  if nc -z 127.0.0.1 8600 2>/dev/null; then
    echo "- Consul DNS is ready (attempt $i/10)"
    break
  fi
  if [ $i -eq 10 ]; then
    echo "- ERROR: Consul DNS not responding after 10 seconds, exiting..."
    exit 1
  fi
  sleep 1
done

# Now restart systemd-resolved to apply Consul DNS configuration
# This must happen AFTER Consul starts, otherwise systemd-resolved marks 127.0.0.1:8600 as unreachable
# Consul DNS (127.0.0.1:8600) is the ONLY DNS server configured in systemd-resolved
# Consul handles ALL queries: .consul directly, everything else via recursor to GCE DNS
echo "[Configuring systemd-resolved for Consul DNS]"
echo "- Restarting systemd-resolved to apply Consul DNS config"
systemctl restart systemd-resolved
echo "- Waiting for systemd-resolved to settle"

# Give Consul a moment to start its DNS server on port 8600
echo "- Waiting for Systemd-resolved to start..."
for i in {1..10}; do
  if host google.com 2>/dev/null; then
    echo "- DNS resolving is ready (attempt $i/10)"
    break
  fi
  if [ $i -eq 10 ]; then
    echo "- ERROR: Systemd-resolved not responding after 10 seconds, exiting..."
    exit 1
  fi
  sleep 1
done
echo "- Flushing DNS caches"
resolvectl flush-caches

%{ if SET_ORCHESTRATOR_VERSION_METADATA == "true" }
# Fetch orchestrator version from Nomad variable via HTTP API (before starting Nomad client)
# This is required - the node cannot start without knowing the orchestrator version
FETCH_TIMEOUT_SECONDS=600
FETCH_INTERVAL_SECONDS=5
FETCH_MAX_ATTEMPTS=$((FETCH_TIMEOUT_SECONDS / FETCH_INTERVAL_SECONDS + 1))

echo "[Fetching orchestrator version from Nomad servers (timeout: $${FETCH_TIMEOUT_SECONDS}s)]"
ORCHESTRATOR_VERSION=""
for i in $(seq 1 $FETCH_MAX_ATTEMPTS); do
  ELAPSED=$(((i - 1) * FETCH_INTERVAL_SECONDS))
  NOMAD_SERVER=$(dig +short nomad.service.consul | head -1)
  if [ -z "$NOMAD_SERVER" ]; then
    echo "- Waiting for Consul DNS (nomad.service.consul)... ($${ELAPSED}s / $${FETCH_TIMEOUT_SECONDS}s)"
  else
    API_RESPONSE=$(curl -s --connect-timeout 5 --max-time 10 -H "X-Nomad-Token: ${NOMAD_TOKEN}" \
      "http://$NOMAD_SERVER:4646/v1/var/nomad/jobs" 2>/dev/null)
    if echo "$API_RESPONSE" | jq -e '.Items.latest_orchestrator_job_id' >/dev/null 2>&1; then
      ORCHESTRATOR_VERSION=$(echo "$API_RESPONSE" | jq -r '.Items.latest_orchestrator_job_id')
      echo "- Fetched orchestrator version: $ORCHESTRATOR_VERSION"
      break
    elif [ -n "$API_RESPONSE" ]; then
      echo "- Invalid response from Nomad API, retrying... ($${ELAPSED}s / $${FETCH_TIMEOUT_SECONDS}s)"
    else
      echo "- No response from Nomad API at $${NOMAD_SERVER}, retrying... ($${ELAPSED}s / $${FETCH_TIMEOUT_SECONDS}s)"
    fi
  fi
  if [ $i -eq $FETCH_MAX_ATTEMPTS ]; then
    echo "- ERROR: Could not fetch orchestrator version from Nomad servers after $${FETCH_TIMEOUT_SECONDS}s"
    echo "- The node cannot start without the orchestrator version. Exiting..."
    exit 1
  fi
  sleep $FETCH_INTERVAL_SECONDS
done

/opt/nomad/bin/run-nomad.sh --client --consul-token "${CONSUL_TOKEN}" --node-pool "${NODE_POOL}" --orchestrator-job-version "$ORCHESTRATOR_VERSION" &
%{ else }
/opt/nomad/bin/run-nomad.sh --client --consul-token "${CONSUL_TOKEN}" --node-pool "${NODE_POOL}" &
%{ endif }

# Add alias for ssh-ing to sbx
echo '_sbx_ssh() {
  local address=$(dig @127.0.0.4 $1. A +short 2>/dev/null)
  ssh -o StrictHostKeyChecking=accept-new "root@$address"
}

alias sbx-ssh=_sbx_ssh' >>/etc/profile
