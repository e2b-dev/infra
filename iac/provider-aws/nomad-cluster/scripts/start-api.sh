#!/usr/bin/env bash
# Start script for API nodes on AWS.

set -euo pipefail

PS4='[\D{%Y-%m-%d %H:%M:%S}] '
set -x

exec > >(tee /var/log/user-data.log | logger -t user-data -s 2>/dev/console) 2>&1

ulimit -n 1048576
export GOMAXPROCS=$(nproc)

sudo tee -a /etc/sysctl.conf <<EOF
net.core.somaxconn = 65535
net.core.netdev_max_backlog = 65535
net.ipv4.tcp_max_syn_backlog = 65535
EOF
sudo sysctl -p

# Download setup scripts from S3
aws s3 cp "s3://${SCRIPTS_BUCKET}/run-consul-${RUN_CONSUL_FILE_HASH}.sh" /opt/consul/bin/run-consul.sh --region "${AWS_REGION}"
aws s3 cp "s3://${SCRIPTS_BUCKET}/run-nomad-${RUN_NOMAD_FILE_HASH}.sh" /opt/nomad/bin/run-nomad.sh --region "${AWS_REGION}"
chmod +x /opt/consul/bin/run-consul.sh /opt/nomad/bin/run-nomad.sh

# Configure Docker auth for ECR
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

mkdir -p /etc/systemd/resolved.conf.d/
cat <<EOF > /etc/systemd/resolved.conf.d/consul.conf
[Resolve]
DNS=127.0.0.1:8600
DNSSEC=false
Domains=~consul
EOF
systemctl restart systemd-resolved

/opt/consul/bin/run-consul.sh --client \
    --consul-token "${CONSUL_TOKEN}" \
    --cluster-tag-name "${CLUSTER_TAG_NAME}" \
    --enable-gossip-encryption \
    --gossip-encryption-key "${CONSUL_GOSSIP_ENCRYPTION_KEY}" \
    --dns-request-token "${CONSUL_DNS_REQUEST_TOKEN}" &

/opt/nomad/bin/run-nomad.sh --client --consul-token "${CONSUL_TOKEN}" --node-pool "${NODE_POOL}" &
