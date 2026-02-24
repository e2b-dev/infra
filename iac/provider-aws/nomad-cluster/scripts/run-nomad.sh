#!/bin/bash
# This script is used to configure and run Nomad on an AWS EC2 Instance.
# AWS-adapted version of the GCP run-nomad.sh script.

set -e
set -x

readonly NOMAD_CONFIG_FILE="default.hcl"
readonly SUPERVISOR_CONFIG_PATH="/etc/supervisor/conf.d/run-nomad.conf"

readonly SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly SCRIPT_NAME="$(basename "$0")"

function log_info { echo "[INFO] [$SCRIPT_NAME] $1"; }
function log_error { echo "[ERROR] [$SCRIPT_NAME] $1"; }

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

# EC2 IMDS v2 metadata access
function get_imds_token {
  curl -s -X PUT "http://169.254.169.254/latest/api/token" -H "X-aws-ec2-metadata-token-ttl-seconds: 300"
}

function get_instance_metadata_value {
  local -r path="$1"
  local token
  token=$(get_imds_token)
  curl -s -H "X-aws-ec2-metadata-token: $token" "http://169.254.169.254/latest/meta-data/$path"
}

function get_instance_region {
  get_instance_metadata_value "placement/region"
}

function get_instance_az {
  get_instance_metadata_value "placement/availability-zone"
}

function get_instance_id {
  get_instance_metadata_value "instance-id"
}

function get_instance_ip_address {
  get_instance_metadata_value "local-ipv4"
}

function generate_nomad_config {
  local -r server="$1"
  local -r client="$2"
  local -r num_servers="$3"
  local -r config_dir="$4"
  local -r user="$5"
  local -r consul_token="$6"
  local -r node_pool="$7"
  local -r orchestrator_job_version="$8"
  local -r config_path="$config_dir/$NOMAD_CONFIG_FILE"

  local instance_id instance_ip_address instance_region az
  instance_id=$(get_instance_id)
  instance_ip_address=$(get_instance_ip_address)
  instance_region=$(get_instance_region)
  az=$(get_instance_az)

  local server_config=""
  if [[ "$server" == "true" ]]; then
    server_config=$(cat <<EOF
server {
  enabled = true
  bootstrap_expect = $num_servers
}
EOF
    )
  fi

  local client_config=""
  if [[ "$client" == "true" ]]; then
    client_config=$(cat <<EOF
client {
  enabled = true
  node_pool = "$node_pool"
  meta {
    "node_pool" = "$node_pool"
    ${orchestrator_job_version:+"\"orchestrator_job_version\"" = "\"$orchestrator_job_version\""}
  }
  max_kill_timeout = "24h"
}

plugin "raw_exec" {
  config {
    enabled = true
    no_cgroups = true
  }
}
EOF
    )
  fi

  cat > "$config_path" <<EOF
datacenter = "$az"
name       = "$instance_id"
region     = "$instance_region"
bind_addr  = "0.0.0.0"

advertise {
  http = "$instance_ip_address"
  rpc  = "$instance_ip_address"
  serf = "$instance_ip_address"
}

leave_on_interrupt = true
leave_on_terminate = true

$client_config

$server_config

plugin_dir = "/opt/nomad/plugins"

plugin "docker" {
  config {
    volumes {
      enabled = true
    }
    auth {
      config = "/root/docker/config.json"
    }
  }
}

log_level = "DEBUG"
log_json = true

telemetry {
  collection_interval = "5s"
  disable_hostname = true
  prometheus_metrics = true
  publish_allocation_metrics = true
  publish_node_metrics = true
}

acl {
  enabled = true
}

limits {
  http_max_conns_per_client = 80
  rpc_max_conns_per_client = 80
}

consul {
  address = "127.0.0.1:8500"
  allow_unauthenticated = false
  token = "$consul_token"
}
EOF
  chown "$user:$user" "$config_path"
}

function generate_supervisor_config {
  local -r supervisor_config_path="$1"
  local -r nomad_config_dir="$2"
  local -r nomad_data_dir="$3"
  local -r nomad_bin_dir="$4"
  local -r nomad_log_dir="$5"
  local nomad_user="$6"
  local -r use_sudo="$7"

  if [[ "$use_sudo" == "true" ]]; then
    nomad_user="root"
  fi

  cat > "$supervisor_config_path" <<EOF
[program:nomad]
command=$nomad_bin_dir/nomad agent -config $nomad_config_dir -data-dir $nomad_data_dir
stdout_logfile=$nomad_log_dir/nomad-stdout.log
stderr_logfile=$nomad_log_dir/nomad-error.log
numprocs=1
autostart=true
autorestart=true
stopsignal=INT
minfds=65536
user=$nomad_user
EOF
}

function start_nomad {
  supervisorctl reread
  supervisorctl update
}

function bootstrap {
  local -r nomad_token="$1"
  while test -z "$(curl -s http://127.0.0.1:4646/v1/agent/health)"; do
    sleep 1
  done

  echo "$nomad_token" > "/tmp/nomad.token"
  nomad acl bootstrap /tmp/nomad.token
  rm "/tmp/nomad.token"
}

function create_node_pools {
  local -r nomad_token="$1"
  local config_dir="/opt/nomad/config"

  cat > "$config_dir/api_node_pool.hcl" <<EOF
node_pool "api" {
  description = "Nodes for api."
}
EOF
  nomad node pool apply -token "$nomad_token" "$config_dir/api_node_pool.hcl"

  cat > "$config_dir/build_node_pool.hcl" <<EOF
node_pool "build" {
  description = "Nodes for template builds."
}
EOF
  nomad node pool apply -token "$nomad_token" "$config_dir/build_node_pool.hcl"
}

function get_owner_of_path {
  local -r path="$1"
  ls -ld "$path" | awk '{print $3}'
}

function run {
  local server="false"
  local client="false"
  local num_servers=""
  local nomad_token=""
  local consul_token=""
  local node_pool="default"
  local orchestrator_job_version=""
  local use_sudo=""

  while [[ $# -gt 0 ]]; do
    local key="$1"
    case "$key" in
      --server) server="true" ;;
      --client) client="true" ;;
      --num-servers) num_servers="$2"; shift ;;
      --nomad-token) nomad_token="$2"; shift ;;
      --consul-token) consul_token="$2"; shift ;;
      --node-pool) node_pool="$2"; shift ;;
      --orchestrator-job-version) orchestrator_job_version="$2"; shift ;;
      --use-sudo) use_sudo="true" ;;
      *) log_error "Unrecognized argument: $key"; exit 1 ;;
    esac
    shift
  done

  if [[ "$server" == "true" ]]; then
    assert_not_empty "--num-servers" "$num_servers"
  fi

  if [[ -z "$use_sudo" ]]; then
    if [[ "$client" == "true" ]]; then use_sudo="true"; else use_sudo="false"; fi
  fi

  local config_dir="/opt/nomad/config"
  local data_dir="/opt/nomad/data"
  local bin_dir="/opt/nomad/bin"
  local log_dir="/opt/nomad/log"
  local user
  user=$(get_owner_of_path "$config_dir")

  generate_nomad_config "$server" "$client" "$num_servers" "$config_dir" "$user" "$consul_token" "$node_pool" "$orchestrator_job_version"
  generate_supervisor_config "$SUPERVISOR_CONFIG_PATH" "$config_dir" "$data_dir" "$bin_dir" "$log_dir" "$user" "$use_sudo"
  start_nomad

  if [[ "$server" == "true" ]]; then
    bootstrap "$nomad_token"
    create_node_pools "$nomad_token"
  fi
}

run "$@"
