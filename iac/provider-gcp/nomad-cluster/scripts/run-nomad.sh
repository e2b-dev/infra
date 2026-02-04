#!/bin/bash
# This script is used to configure and run Nomad on a Google Compute Instance.

set -e

# Enable command tracing
set -x

readonly NOMAD_CONFIG_FILE="default.hcl"
readonly SUPERVISOR_CONFIG_PATH="/etc/supervisor/conf.d/run-nomad.conf"

readonly COMPUTE_INSTANCE_METADATA_URL="http://metadata.google.internal/computeMetadata/v1"
readonly GOOGLE_CLOUD_METADATA_REQUEST_HEADER="Metadata-Flavor: Google"

readonly SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly SCRIPT_NAME="$(basename "$0")"

function print_usage {
  echo
  echo "Usage: run-nomad [OPTIONS]"
  echo
  echo "This script is used to configure and run Nomad on a Google Compute Instance."
  echo
  echo "Options:"
  echo
  echo -e "  --server\t\tIf set, run in server mode. Optional. At least one of --server or --client must be set."
  echo -e "  --client\t\tIf set, run in client mode. Optional. At least one of --server or --client must be set."
  echo -e "  --num-servers\t\tThe minimum number of servers to expect in the Nomad cluster. Required if --server is true."
  echo -e "  --consul-token\t\tThe ACL token that Consul uses."
  echo -e "  --nomad-token\t\tThe Nomad ACL token to use."
  echo -e "  --config-dir\t\tThe path to the Nomad config folder. Optional. Default is the absolute path of '../config', relative to this script."
  echo -e "  --data-dir\t\tThe path to the Nomad data folder. Optional. Default is the absolute path of '../data', relative to this script."
  echo -e "  --bin-dir\t\tThe path to the folder with Nomad binary. Optional. Default is the absolute path of the parent folder of this script."
  echo -e "  --log-dir\t\tThe path to the Nomad log folder. Optional. Default is the absolute path of '../log', relative to this script."
  echo -e "  --user\t\tThe user to run Nomad as. Optional. Default is to use the owner of --config-dir."
  echo -e "  --use-sudo\t\tIf set, run the Nomad agent with sudo. By default, sudo is only used if --client is set."
  echo -e "  --skip-nomad-config\tIf this flag is set, don't generate a Nomad configuration file. Optional. Default is false."
  echo -e "  --api\t\tIf set, run the Nomad agent dedicated to API. Optional. Default is false."
  echo -e "  --orchestrator-job-version\tThe orchestrator job version to set as node metadata. Optional."
  echo
  echo "Example:"
  echo
  echo "  run-nomad.sh --server --config-dir /custom/path/to/nomad/config"
}

function log {
  local -r level="$1"
  local -r message="$2"
  local -r timestamp=$(date +"%Y-%m-%d %H:%M:%S")
  echo >&2 -e "${timestamp} [${level}] [$SCRIPT_NAME] ${message}"
}

function log_info {
  local -r message="$1"
  log "INFO" "$message"
}

function log_warn {
  local -r message="$1"
  log "WARN" "$message"
}

function log_error {
  local -r message="$1"
  log "ERROR" "$message"
}

# Based on code from: http://stackoverflow.com/a/16623897/483528
function strip_prefix {
  local -r str="$1"
  local -r prefix="$2"
  echo "${str#$prefix}"
}

function assert_not_empty {
  local -r arg_name="$1"
  local -r arg_value="$2"

  if [[ -z "$arg_value" ]]; then
    log_error "The value for '$arg_name' cannot be empty"
    print_usage
    exit 1
  fi
}

# Get the value at a specific Instance Metadata path.
function get_instance_metadata_value {
  local -r path="$1"

  log_info "Looking up Metadata value at $COMPUTE_INSTANCE_METADATA_URL/$path"
  response=$(curl --silent --show-error --location \
    --header "$GOOGLE_CLOUD_METADATA_REQUEST_HEADER" \
    --write-out "%{http_code}" \
    --output /tmp/metadata.out \
    "$COMPUTE_INSTANCE_METADATA_URL/$path")

  if [ "$response" -eq 200 ]; then
    cat /tmp/metadata.out
  else
    echo ""
  fi
  rm  /tmp/metadata.out
}

# Get the value of the given Custom Metadata Key
function get_instance_custom_metadata_value {
  local -r key="$1"

  log_info "Looking up Custom Instance Metadata value for key \"$key\""
  get_instance_metadata_value "instance/attributes/$key"
}

# Get the ID of the Project in which this Compute Instance currently resides
function get_instance_project_id {
  log_info "Looking up Project ID"
  get_instance_metadata_value "project/project-id"
}

# Get the GCE Zone in which this Compute Instance currently resides
function get_instance_zone {
  log_info "Looking up Zone of the current Compute Instance"

  # The value returned for zone will be of the form "projects/121238320500/zones/us-west1-a" so we need to split the string
  # by "/" and return the 4th string.
  get_instance_metadata_value "instance/zone" | cut -d'/' -f4
}

function get_instance_region {
  # Trim the last two characters of a zone (e.g. us-west1-a) to get the region (e.g. us-west1)
  # Head command comes from https://unix.stackexchange.com/a/215156/129208
  get_instance_zone | head -c -3
}

# Get the ID of the current Compute Instance
function get_instance_name {
  log_info "Looking up current Compute Instance name"
  get_instance_metadata_value "instance/name"
}

# Get the IP Address of the current Compute Instance
function get_instance_ip_address {
  local network_interface_number="$1"

  # If no network interface number was specified, default to the first one
  if [[ -z "$network_interface_number" ]]; then
    network_interface_number=0
  fi

  log_info "Looking up Compute Instance IP Address on Network Interface $network_interface_number"
  get_instance_metadata_value "instance/network-interfaces/$network_interface_number/ip"
}

function assert_is_installed {
  local -r name="$1"

  if [[ ! $(command -v ${name}) ]]; then
    log_error "The binary '$name' is required by this script but is not installed or in the system's PATH."
    exit 1
  fi
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

  local instance_name=""
  local instance_ip_address=""
  local instance_region=""
  local instance_zone=""
  local job_constraint=""

  instance_name=$(get_instance_name)
  instance_ip_address=$(get_instance_ip_address)
  instance_region=$(get_instance_region)
  zone=$(get_instance_zone)
  job_constraint=$(get_instance_custom_metadata_value "job-constraint" || true)

  local server_config=""
  if [[ "$server" == "true" ]]; then
    server_config=$(
      cat <<EOF
server {
  enabled = true
  bootstrap_expect = $num_servers
}

EOF
    )
  fi

  local client_config=""
  if [[ "$client" == "true" ]]; then
    client_config=$(
      cat <<EOF
client {
  enabled = true
  node_pool = "$node_pool"
  meta {
    "node_pool" = "$node_pool"
    ${job_constraint:+"\"job_constraint\"" = "\"$job_constraint\""}
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

  log_info "Creating default Nomad config file in $config_path"
  cat >"$config_path" <<EOF
datacenter = "$zone"
name       = "$instance_name"
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
    log_info "The --use-sudo flag is set, so running Nomad as the root user"
    nomad_user="root"
  fi

  log_info "Creating Supervisor config file to run Nomad in $supervisor_config_path"
  cat >"$supervisor_config_path" <<EOF
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
  log_info "Reloading Supervisor config and starting Nomad"
  supervisorctl reread
  supervisorctl update
}

function bootstrap {
  log_info "Waiting for Nomad to start"
  while test -z "$(curl -s http://127.0.0.1:4646/v1/agent/health)"; do
    log_info "Nomad not yet started. Waiting for 1 second."
    sleep 1
  done
  log_info "Nomad server started."

  local -r nomad_token="$1"
  log_info "Bootstrapping Nomad"
  echo "$nomad_token" >"/tmp/nomad.token"
  nomad acl bootstrap /tmp/nomad.token
  rm "/tmp/nomad.token"
}

function create_node_pools {
  local -r nomad_token="$1"
  log_info "Creating node pools"
  cat > "$config_dir/api_node_pool.hcl"  <<EOF
node_pool "api" {
  description = "Nodes for api."
}
EOF
  nomad node pool apply -token "$nomad_token" "$config_dir/api_node_pool.hcl"
  cat > "$config_dir/build_node_pool.hcl"  <<EOF
node_pool "build" {
  description = "Nodes for template builds."
}
EOF
  nomad node pool apply -token "$nomad_token" "$config_dir/build_node_pool.hcl"
}

# Based on: http://unix.stackexchange.com/a/7732/215969
function get_owner_of_path {
  local -r path="$1"
  ls -ld "$path" | awk '{print $3}'
}

function run {
  local server="false"
  local client="false"
  local num_servers=""
  local all_args=()

  while [[ $# > 0 ]]; do
    local key="$1"

    case "$key" in
    --server)
      server="true"
      ;;
    --client)
      client="true"
      ;;
    --num-servers)
      num_servers="$2"
      shift
      ;;
    --nomad-token)
      assert_not_empty "$key" "$2"
      nomad_token="$2"
      shift
      ;;
    --consul-token)
      assert_not_empty "$key" "$2"
      consul_token="$2"
      shift
      ;;
    --node-pool)
      node_pool="$2"
      shift
      ;;
    --orchestrator-job-version)
      orchestrator_job_version="$2"
      shift
      ;;
    --cluster-tag-value)
      assert_not_empty "$key" "$2"
      cluster_tag_value="$2"
      shift
      ;;
    --use-sudo)
      use_sudo="true"
      ;;
    *)
      log_error "Unrecognized argument: $key"
      print_usage
      exit 1
      ;;
    esac

    shift
  done

  if [[ "$server" == "true" ]]; then
    assert_not_empty "--num-servers" "$num_servers"
  fi

  if [[ "$server" == "false" && "$client" == "false" ]]; then
    log_error "At least one of --server or --client must be set"
    exit 1
  fi

  if [[ -z "$use_sudo" ]]; then
    if [[ "$client" == "true" ]]; then
      use_sudo="true"
    else
      use_sudo="false"
    fi
  fi

  assert_is_installed "supervisorctl"
  assert_is_installed "curl"

  config_dir=$(cd "$SCRIPT_DIR/../config" && pwd)

  data_dir=$(cd "$SCRIPT_DIR/../data" && pwd)

  bin_dir=$(cd "$SCRIPT_DIR/../bin" && pwd)

  log_dir=$(cd "$SCRIPT_DIR/../log" && pwd)

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
