#!/bin/bash
# This script is used to configure and run Consul on an AWS EC2 Instance.
# AWS-adapted version of the GCP run-consul.sh script.

set -e
set -x

readonly CONSUL_CONFIG_FILE="default.json"
readonly SYSTEMD_CONFIG_PATH="/etc/systemd/system/consul.service"

readonly SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

readonly DEFAULT_RAFT_PROTOCOL="3"
readonly DEFAULT_AUTOPILOT_CLEANUP_DEAD_SERVERS="true"
readonly DEFAULT_AUTOPILOT_LAST_CONTACT_THRESHOLD="200ms"
readonly DEFAULT_AUTOPILOT_MAX_TRAILING_LOGS="250"
readonly DEFAULT_AUTOPILOT_SERVER_STABILIZATION_TIME="10s"
readonly DEFAULT_AUTOPILOT_REDUNDANCY_ZONE_TAG="az"
readonly DEFAULT_AUTOPILOT_DISABLE_UPGRADE_MIGRATION="false"

function log_info { echo "[INFO] $1"; }
function log_warn { echo "[WARN] $1"; }
function log_error { echo "[ERROR] $1"; }

function assert_not_empty {
  local -r arg_name="$1"
  local -r arg_value="$2"
  if [[ -z "$arg_value" ]]; then
    log_error "The value for '$arg_name' cannot be empty"
    exit 1
  fi
}

function assert_is_installed {
  local -r name="$1"
  if [[ ! $(command -v ${name}) ]]; then
    log_error "The binary '$name' is required but is not installed."
    exit 1
  fi
}

# EC2 IMDS v2 token-based metadata access
function get_imds_token {
  curl -s -X PUT "http://169.254.169.254/latest/api/token" -H "X-aws-ec2-metadata-token-ttl-seconds: 300"
}

function get_instance_metadata_value {
  local -r path="$1"
  local token
  token=$(get_imds_token)
  curl -s -H "X-aws-ec2-metadata-token: $token" "http://169.254.169.254/latest/meta-data/$path"
}

function get_instance_tag_value {
  local -r key="$1"
  local token
  token=$(get_imds_token)
  curl -s -H "X-aws-ec2-metadata-token: $token" "http://169.254.169.254/latest/meta-data/tags/instance/$key" 2>/dev/null || echo ""
}

function get_instance_region {
  get_instance_metadata_value "placement/region"
}

function get_instance_id {
  get_instance_metadata_value "instance-id"
}

function get_instance_ip_address {
  get_instance_metadata_value "local-ipv4"
}

function generate_consul_config {
  local -r server="${1}"
  local -r consul_token="${2}"
  local -r config_dir="${3}"
  local -r user="${4}"
  local -r cluster_tag_name="${5}"
  local -r datacenter="${6}"
  local -r enable_gossip_encryption="${7}"
  local -r gossip_encryption_key="${8}"
  local -r cluster_size="${9}"
  local -r config_path="$config_dir/$CONSUL_CONFIG_FILE"

  shift 9
  local -ar recursors=("$@")

  local instance_id instance_ip_address instance_region
  instance_ip_address=$(get_instance_ip_address)
  instance_id=$(get_instance_id)
  instance_region=$(get_instance_region)

  local retry_join_json=""
  if [[ -n "$cluster_tag_name" ]]; then
    retry_join_json="\"retry_join\": [\"provider=aws tag_key=ClusterTag tag_value=$cluster_tag_name region=$instance_region\"],"
  fi

  local recursors_config=""
  if [[ ${#recursors[@]} -ne 0 ]]; then
    recursors_config="\"recursors\" : [ "
    for recursor in "${recursors[@]}"; do
      recursors_config="${recursors_config}\"${recursor}\", "
    done
    recursors_config=$(echo "${recursors_config}" | sed 's/, $//')" ],"
  fi

  local bootstrap_expect=""
  local ui="false"
  if [[ "$server" == "true" ]]; then
    bootstrap_expect="\"bootstrap_expect\": $cluster_size,"
    ui="true"
  fi

  local gossip_encryption_configuration=""
  if [[ "$enable_gossip_encryption" == "true" && -n "$gossip_encryption_key" ]]; then
    gossip_encryption_configuration="\"encrypt\": \"$gossip_encryption_key\","
  fi

  local default_config_json
  default_config_json=$(cat <<EOF
{
  "connect": { "enabled": true },
  "acl": {
    "enabled": true,
    "default_policy": "deny",
    "enable_token_persistence": true,
    "tokens": { "default": "$consul_token" }
  },
  "telemetry": {
    "prometheus_retention_time": "2h",
    "disable_hostname": true
  },
  "limits": { "http_max_conns_per_client": 80 },
  "advertise_addr": "$instance_ip_address",
  "bind_addr": "$instance_ip_address",
  $bootstrap_expect
  "client_addr": "0.0.0.0",
  "datacenter": "$datacenter",
  "node_name": "$instance_id",
  "leave_on_terminate": true,
  "skip_leave_on_interrupt": true,
  $recursors_config
  $retry_join_json
  "server": $server,
  $gossip_encryption_configuration
  "autopilot": {
    "cleanup_dead_servers": true,
    "last_contact_threshold": "200ms",
    "max_trailing_logs": 250,
    "server_stabilization_time": "10s"
  },
  "ui": $ui
}
EOF
  )

  echo "$default_config_json" | jq '.' > "$config_path"
  chown "$user:$user" "$config_path"
}

function generate_systemd_config {
  local -r systemd_config_path="$1"
  local -r consul_config_dir="$2"
  local -r consul_data_dir="$3"
  local -r consul_bin_dir="$4"
  local -r consul_user="$5"
  local -r config_path="$consul_config_dir/$CONSUL_CONFIG_FILE"

  cat > "$systemd_config_path" <<EOF
[Unit]
Description="HashiCorp Consul - A service mesh solution"
Documentation=https://www.consul.io/
Requires=network-online.target
After=network-online.target
ConditionFileNotEmpty=$config_path

[Service]
Type=notify
User=$consul_user
Group=$consul_user
ExecStart=$consul_bin_dir/consul agent -config-dir $consul_config_dir -data-dir $consul_data_dir
ExecReload=$consul_bin_dir/consul reload
ExecStop=$consul_bin_dir/consul leave
KillMode=process
Restart=on-failure
TimeoutSec=300s
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
EOF
}

function start_consul {
  sudo systemctl daemon-reload
  sudo systemctl enable consul.service
  sudo systemctl restart consul.service
}

function bootstrap {
  local consul_token="$1"
  local instance_ip_address
  instance_ip_address=$(get_instance_ip_address)

  while true; do
    consul_leader_addr=$(curl -s http://localhost:8500/v1/status/leader 2>/dev/null || true)
    if [[ "$consul_leader_addr" == "\"$instance_ip_address:8300\"" ]]; then
      echo "${consul_token}" > /tmp/consul.token
      consul acl bootstrap /tmp/consul.token
      rm /tmp/consul.token
      break
    fi
    if [[ -n "$consul_leader_addr" && "$consul_leader_addr" != "\"\"" ]]; then
      break
    fi
    sleep 1
  done
}

function setup_dns_resolving {
  local consul_token="$1"
  local dns_request_token="$2"

  until consul info -token="${consul_token}" > /dev/null 2>&1; do
    sleep 1
  done

  if (($(consul acl policy read -name="dns-request-policy" -token="${consul_token}" -format=json 2>/dev/null | jq '.ID' | wc -l) > 0)); then
    return
  else
    cat <<EOF > dns-request-policy.hcl
node_prefix "" { policy = "read" }
service_prefix "" { policy = "read" }
EOF
    cat <<EOF > register-service-policy.hcl
service_prefix "" { policy = "write" }
EOF
    consul acl policy create -name "dns-request-policy" -rules @dns-request-policy.hcl -token="${consul_token}" || true
    consul acl policy create -name "register-service-policy" -rules @register-service-policy.hcl -token="${consul_token}" || true
    consul acl token create -secret "${dns_request_token}" -description "Client Token" -policy-name "dns-request-policy" -policy-name "register-service-policy" -token="${consul_token}" || true
    rm -f dns-request-policy.hcl register-service-policy.hcl
  fi

  consul acl set-agent-token -token="${consul_token}" default "${dns_request_token}"
}

function get_owner_of_path {
  local -r path="$1"
  ls -ld "$path" | awk '{print $3}'
}

function run {
  local server="false"
  local client="false"
  local consul_token=""
  local cluster_tag_name=""
  local datacenter=""
  local enable_gossip_encryption="false"
  local gossip_encryption_key=""
  local dns_request_token=""
  local cluster_size="3"
  local recursors=()

  while [[ $# -gt 0 ]]; do
    local key="$1"
    case "$key" in
      --server) server="true" ;;
      --client) client="true" ;;
      --consul-token) consul_token="$2"; shift ;;
      --cluster-tag-name) cluster_tag_name="$2"; shift ;;
      --datacenter) datacenter="$2"; shift ;;
      --cluster-size) cluster_size="$2"; shift ;;
      --enable-gossip-encryption) enable_gossip_encryption="true" ;;
      --gossip-encryption-key) gossip_encryption_key="$2"; shift ;;
      --dns-request-token) dns_request_token="$2"; shift ;;
      --recursor) recursors+=("$2"); shift ;;
      *) log_error "Unrecognized argument: $key"; exit 1 ;;
    esac
    shift
  done

  if [[ -z "$datacenter" ]]; then
    datacenter=$(get_instance_region)
  fi

  local config_dir="/opt/consul/config"
  local data_dir="/opt/consul/data"
  local bin_dir="/opt/consul/bin"
  local user
  user=$(get_owner_of_path "$config_dir")

  generate_consul_config "$server" "$consul_token" "$config_dir" "$user" "$cluster_tag_name" "$datacenter" "$enable_gossip_encryption" "$gossip_encryption_key" "$cluster_size" "${recursors[@]}"
  generate_systemd_config "$SYSTEMD_CONFIG_PATH" "$config_dir" "$data_dir" "$bin_dir" "$user"
  start_consul

  if [[ "$client" == "true" ]]; then
    setup_dns_resolving "$consul_token" "$dns_request_token"
  fi

  if [[ "$server" == "true" ]]; then
    bootstrap "$consul_token"
  fi
}

run "$@"
