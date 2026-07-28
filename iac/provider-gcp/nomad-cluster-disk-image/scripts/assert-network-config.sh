#!/usr/bin/env bash
set -euo pipefail

config_path="${1:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)/main.tf}"

[[ -f "${config_path}" ]] || {
  printf 'Packer network Terraform configuration is missing: %s\n' \
    "${config_path}" >&2
  exit 1
}

normalized_config="$(sed -E 's/[[:space:]]+//g' "${config_path}")"

require_assignment() {
  local assignment="$1"
  local description="$2"
  local count

  count="$(grep -Foc "${assignment}" <<<"${normalized_config}" || true)"
  if [[ "${count}" -ne 1 ]]; then
    printf 'Network configuration must contain exactly one %s assignment: %s\n' \
      "${description}" "${assignment}" >&2
    exit 1
  fi
}

require_assignment_count() {
  local assignment="$1"
  local expected_count="$2"
  local description="$3"
  local count

  count="$(grep -Foc "${assignment}" <<<"${normalized_config}" || true)"
  if [[ "${count}" -ne "${expected_count}" ]]; then
    printf 'Network configuration must contain exactly %s %s assignments: %s\n' \
      "${expected_count}" "${description}" "${assignment}" >&2
    exit 1
  fi
}

if [[ "$(grep -Ec '^resource "[^"]+" "[^"]+" \{' "${config_path}")" -ne 3 ]]; then
  printf 'Network configuration must declare exactly three managed resources.\n' >&2
  exit 1
fi

for declaration in \
  'resource "google_compute_network" "packer_network" {' \
  'resource "google_compute_subnetwork" "packer_subnetwork" {' \
  'resource "google_compute_firewall" "internal_remote_connection_firewall_ingress" {'; do
  if [[ "$(grep -Fxc "${declaration}" "${config_path}")" -ne 1 ]]; then
    printf 'Missing or duplicate reviewed resource declaration: %s\n' \
      "${declaration}" >&2
    exit 1
  fi
done

if grep -Eq '^[[:space:]]*(data|module|import|removed)[[:space:]]+"' \
  "${config_path}"; then
  printf 'Network configuration must not declare data sources, modules, imports, or removed blocks.\n' >&2
  exit 1
fi

if grep -Eqi \
  '(secret_manager|service_account_key|credentials[[:space:]]*=|local-exec|remote-exec)' \
  "${config_path}"; then
  printf 'Network configuration contains an unreviewed credential or execution seam.\n' >&2
  exit 1
fi

require_assignment 'required_version="=1.7.5"' 'Terraform version'
require_assignment 'prefix="terraform/cluster-disk-image/state"' 'backend prefix'
require_assignment_count 'project=var.gcp_project_id' 4 'provider/resource project'
require_assignment 'name=var.network_name' 'network name'
require_assignment 'auto_create_subnetworks=false' 'custom-mode VPC'
require_assignment 'delete_default_routes_on_create=false' 'default-route policy'
require_assignment 'enable_ula_internal_ipv6=false' 'IPv6 policy'
require_assignment 'mtu=1460' 'network MTU'
require_assignment \
  'network_firewall_policy_enforcement_order="AFTER_CLASSIC_FIREWALL"' \
  'firewall-policy order'
require_assignment 'routing_mode="REGIONAL"' 'routing mode'
require_assignment 'ip_cidr_range="10.0.0.0/8"' 'subnet CIDR'
require_assignment 'name=var.subnet_name' 'subnet name'
require_assignment_count 'region=var.gcp_region' 2 'provider/subnet region'
require_assignment 'network=google_compute_network.packer_network.id' 'subnet network binding'
require_assignment 'private_ip_google_access=false' 'private Google access policy'
require_assignment \
  'send_secondary_ip_range_if_empty=false' \
  'secondary range policy'
require_assignment 'stack_type="IPV4_ONLY"' 'subnet IP stack'
require_assignment 'aggregation_interval="INTERVAL_15_MIN"' 'flow-log interval'
require_assignment 'flow_sampling=0' 'flow-log sampling'
require_assignment 'metadata="EXCLUDE_ALL_METADATA"' 'flow-log metadata policy'
require_assignment 'name="${var.network_name}-firewall-ingress"' 'firewall name'
require_assignment 'network=google_compute_network.packer_network.name' 'firewall network binding'
require_assignment 'disabled=false' 'firewall enablement'
require_assignment 'destination_ranges=[]' 'firewall destination ranges'
require_assignment 'protocol="tcp"' 'firewall protocol'
require_assignment 'ports=["22"]' 'firewall port'
require_assignment 'priority=900' 'firewall priority'
require_assignment 'direction="INGRESS"' 'firewall direction'
require_assignment 'source_ranges=["35.235.240.0/20"]' 'IAP source range'

if [[ "$(grep -Foc 'prevent_destroy=true' <<<"${normalized_config}")" -ne 3 ]]; then
  printf 'All three network resources must retain lifecycle.prevent_destroy.\n' >&2
  exit 1
fi

printf 'Packer network configuration matches the reviewed static topology.\n'
