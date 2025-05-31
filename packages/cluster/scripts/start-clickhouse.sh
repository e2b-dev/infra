#!/usr/bin/env bash

set -euo pipefail


# Set timestamp format
PS4='[\D{%Y-%m-%d %H:%M:%S}] '
# Enable command tracing
set -x

# Send the log output from this script to user-data.log, syslog, and the console
# Inspired by https://alestic.com/2010/12/ec2-user-data-output/
exec > >(tee /var/log/user-data.log | logger -t user-data -s 2>/dev/console) 2>&1

ulimit -n 1048576

# Get the GCP instance name
INSTANCE_NAME=$(curl -s "http://metadata.google.internal/computeMetadata/v1/instance/name" -H "Metadata-Flavor: Google")

# Define the disk and mount point
DISK="/dev/disk/by-id/google-$INSTANCE_NAME-disk"
MOUNT_POINT="/clickhouse"

# Create filesystem if not already formatted
if ! blkid $DISK; then
  mkfs.xfs -f -b size=4096 $DISK
fi

# Create mount point
mkdir -p $MOUNT_POINT

# Mount the disk
mount -o noatime $DISK $MOUNT_POINT

sudo tee -a /etc/sysctl.conf <<EOF
# Increase the maximum number of socket connections
net.core.somaxconn = 65535

# Increase the maximum number of backlogged connections
net.core.netdev_max_backlog = 65535

# Increase maximum number of TCP sockets
net.ipv4.tcp_max_syn_backlog = 65535
EOF
sudo sysctl -p

# These variables are passed in via Terraform template interpolation
gsutil cp "gs://${SCRIPTS_BUCKET}/run-consul-${RUN_CONSUL_FILE_HASH}.sh" /opt/consul/bin/run-consul.sh
gsutil cp "gs://${SCRIPTS_BUCKET}/${RUN_NOMAD_FILE_NAME}-${RUN_NOMAD_FILE_HASH}.sh" /opt/nomad/bin/run-nomad.sh
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
Domains=~consul
EOF

# Expose systemd-resolved’s DNS stub on the Docker bridge so that
# containers can resolve *.consul names.
#
# Context
# -----------------
# systemd-resolved already forwards *.consul → 127.0.0.1:8600
# (configured in /etc/systemd/resolved.conf.d/consul.conf).
# When the host’s /etc/resolv.conf contains only 127.0.0.53,
# Docker copies /run/systemd/resolve/resolve.conf into every container.
# 127.0.0.53 is bound only to the host loopback interface,
# so DNS look-ups fail inside containers on the default bridge network.
#
# Fix
# -----------------
# Make the stub resolver listen on docker0 (typically 172.17.0.1) via DNSStubListenerExtra
# Tell Docker to use that address (Nomad job config):
# network {
#   mode = "bridge"
#     dns {
#       servers = ["172.17.0.1", "8.8.8.8", "8.8.4.4", "169.254.169.254"]
#   }
# }
#
# Ref: https://web.archive.org/web/20250529054655/https://felix.ehrenpfort.de/notes/2022-06-22-use-consul-dns-interface-inside-docker-container/
touch /etc/systemd/resolved.conf.d/docker.conf
cat <<EOF >/etc/systemd/resolved.conf.d/docker.conf
[Resolve]
DNSStubListener=yes
DNSStubListenerExtra=172.17.0.1
EOF
systemctl restart systemd-resolved

# These variables are passed in via Terraform template interpolation
/opt/consul/bin/run-consul.sh --client \
    --consul-token "${CONSUL_TOKEN}" \
    --cluster-tag-name "${CLUSTER_TAG_NAME}" \
    --enable-gossip-encryption \
    --gossip-encryption-key "${CONSUL_GOSSIP_ENCRYPTION_KEY}" \
    --dns-request-token "${CONSUL_DNS_REQUEST_TOKEN}" &

/opt/nomad/bin/run-nomad.sh --consul-token "${CONSUL_TOKEN}" &

# Install clickhouse client
cd /usr/local/bin && curl https://clickhouse.com/ | sh && sudo ./clickhouse install &
