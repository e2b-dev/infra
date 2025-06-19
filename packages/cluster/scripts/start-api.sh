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

ulimit -n 1048576
export GOMAXPROCS='nproc'

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
gsutil cp "gs://${SCRIPTS_BUCKET}/run-api-nomad-${RUN_NOMAD_FILE_HASH}.sh" /opt/nomad/bin/run-nomad.sh
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

# Configure dnsmasq for Consul DNS routing
# Detect the current DNS server dynamically
PRIMARY_INTERFACE=$(ip route show default | awk '/default/ { print $5 }' | head -1)
CLOUD_DNS=$(resolvectl status "$PRIMARY_INTERFACE" 2>/dev/null | grep "DNS Servers:" | sed 's/.*DNS Servers: *//' | awk '{print $1}')

# Configure dnsmasq for domain-based DNS forwarding
cat > /etc/dnsmasq.d/consul.conf << EOF
# Use port 5353 to avoid conflict with systemd-resolved
port=5353

# Forward .consul domains to Consul DNS
server=/consul/127.0.0.1#8600

# Forward everything else to detected cloud DNS server
server=${CLOUD_DNS}

# Configuration
no-resolv
no-hosts
listen-address=127.0.0.1
no-dhcp-interface=
EOF

mkdir -p /etc/systemd/resolved.conf.d
# Configure systemd-resolved to use dnsmasq
cat > /etc/systemd/resolved.conf.d/dnsmasq.conf << EOF
[Resolve]
DNS=127.0.0.1:5353
DNSSEC=false
FallbackDNS=
EOF

# Start services
systemctl restart dnsmasq
systemctl restart systemd-resolved


# These variables are passed in via Terraform template interpolation
/opt/consul/bin/run-consul.sh --client \
    --consul-token "${CONSUL_TOKEN}" \
    --cluster-tag-name "${CLUSTER_TAG_NAME}" \
    --enable-gossip-encryption \
    --gossip-encryption-key "${CONSUL_GOSSIP_ENCRYPTION_KEY}" \
    --dns-request-token "${CONSUL_DNS_REQUEST_TOKEN}" &

/opt/nomad/bin/run-nomad.sh --consul-token "${CONSUL_TOKEN}" &
