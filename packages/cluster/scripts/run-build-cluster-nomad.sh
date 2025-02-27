#!/bin/bash
# This script is used to configure and run Nomad on a Google Compute Instance.

set -e

readonly NOMAD_CONFIG_FILE="default.hcl"
readonly SUPERVISOR_CONFIG_PATH="/etc/supervisor/conf.d/run-nomad.conf"

readonly COMPUTE_INSTANCE_METADATA_URL="http://metadata.google.internal/computeMetadata/v1"
readonly GOOGLE_CLOUD_METADATA_REQUEST_HEADER="Metadata-Flavor: Google"

readonly SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly SCRIPT_NAME="$(basename "$0")"

function log {
  local readonly level="$1"
  local readonly message="$2"
  local readonly timestamp=$(date +"%Y-%m-%d %H:%M:%S")
  echo >&2 -e "${timestamp} [${level}] [$SCRIPT_NAME] ${message}"
}

function log_info {
  local readonly message="$1"
  log "INFO" "$message"
}

function log_warn {
  local readonly message="$1"
  log "WARN" "$message"
}

function log_error {
  local readonly message="$1"
  log "ERROR" "$message"
}

# Based on code from: http://stackoverflow.com/a/16623897/483528
function strip_prefix {
  local readonly str="$1"
  local readonly prefix="$2"
  echo "${str#$prefix}"
}

function assert_not_empty {
  local readonly arg_name="$1"
  local readonly arg_value="$2"

  if [[ -z "$arg_value" ]]; then
    log_error "The value for '$arg_name' cannot be empty"
    print_usage
    exit 1
  fi
}

# Get the value at a specific Instance Metadata path.
function get_instance_metadata_value {
  local readonly path="$1"

  log_info "Looking up Metadata value at $COMPUTE_INSTANCE_METADATA_URL/$path"
  curl --silent --show-error --location --header "$GOOGLE_CLOUD_METADATA_REQUEST_HEADER" "$COMPUTE_INSTANCE_METADATA_URL/$path"
}

# Get the value of the given Custom Metadata Key
function get_instance_custom_metadata_value {
  local readonly key="$1"

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
  local readonly name="$1"

  if [[ ! $(command -v ${name}) ]]; then
    log_error "The binary '$name' is required by this script but is not installed or in the system's PATH."
    exit 1
  fi
}

function generate_nomad_config {
  local readonly server="$1"
  local readonly client="$2"
  local readonly num_servers="$3"
  local readonly config_dir="$4"
  local readonly user="$5"
  local readonly consul_token="$6"
  local readonly config_path="$config_dir/$NOMAD_CONFIG_FILE"

  local instance_name=""
  local instance_ip_address=""
  local instance_region=""
  local instance_zone=""

  instance_name=$(get_instance_name)
  instance_ip_address=$(get_instance_ip_address)
  instance_region=$(get_instance_region)
  zone=$(get_instance_zone)


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

client {
  enabled = true
  node_pool = "build"
  meta {
    "node_pool" = "build"
  }
  max_kill_timeout = "24h"
}

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


plugin "raw_exec" {
  config {
    enabled = true
    no_cgroups = true
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
  local readonly supervisor_config_path="$1"
  local readonly nomad_config_dir="$2"
  local readonly nomad_data_dir="$3"
  local readonly nomad_bin_dir="$4"
  local readonly nomad_log_dir="$5"
  local readonly nomad_user="$6"
  local readonly use_sudo="$7"

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

  local readonly nomad_token="$1"
  log_info "Bootstrapping Nomad"
  echo "$nomad_token" >"/tmp/nomad.token"
  nomad acl bootstrap /tmp/nomad.token
  rm "/tmp/nomad.token"
}

# Based on: http://unix.stackexchange.com/a/7732/215969
function get_owner_of_path {
  local readonly path="$1"
  ls -ld "$path" | awk '{print $3}'
}

function run {
  local nodepool="default"
  local all_args=()

  while [[ $# > 0 ]]; do
    local key="$1"

    case "$key" in
    --consul-token)
      assert_not_empty "$key" "$2"
      consul_token="$2"
      shift
      ;;
    *)
      log_error "Unrecognized argument: $key"
      print_usage
      exit 1
      ;;
    esac

    shift
  done



  use_sudo="true"

  assert_is_installed "supervisorctl"
  assert_is_installed "curl"

  config_dir=$(cd "$SCRIPT_DIR/../config" && pwd)

  data_dir=$(cd "$SCRIPT_DIR/../data" && pwd)

  bin_dir=$(cd "$SCRIPT_DIR/../bin" && pwd)

  log_dir=$(cd "$SCRIPT_DIR/../log" && pwd)

  user=$(get_owner_of_path "$config_dir")

  generate_nomad_config "$server" "$client" "$num_servers" "$config_dir" "$user" "$consul_token"
  generate_supervisor_config "$SUPERVISOR_CONFIG_PATH" "$config_dir" "$data_dir" "$bin_dir" "$log_dir" "$user" "$use_sudo"
  start_nomad
}

run "$@"
