#!/usr/bin/env bash
set -euo pipefail

plan_path="${1:?usage: assert-network-plan.sh PLAN TERRAFORM_BIN PROJECT REGION NETWORK SUBNET}"
terraform_bin="${2:?usage: assert-network-plan.sh PLAN TERRAFORM_BIN PROJECT REGION NETWORK SUBNET}"
project_id="${3:?usage: assert-network-plan.sh PLAN TERRAFORM_BIN PROJECT REGION NETWORK SUBNET}"
region="${4:?usage: assert-network-plan.sh PLAN TERRAFORM_BIN PROJECT REGION NETWORK SUBNET}"
network_name="${5:?usage: assert-network-plan.sh PLAN TERRAFORM_BIN PROJECT REGION NETWORK SUBNET}"
subnet_name="${6:?usage: assert-network-plan.sh PLAN TERRAFORM_BIN PROJECT REGION NETWORK SUBNET}"

command -v jq >/dev/null 2>&1 || {
  printf 'jq is required to inspect the saved network plan.\n' >&2
  exit 1
}

if [[ ! -f "${plan_path}" || -L "${plan_path}" ]]; then
  printf 'Saved network plan must be a regular, non-symlink file: %s\n' \
    "${plan_path}" >&2
  exit 1
fi

plan_json="$("${terraform_bin}" show -json "${plan_path}")"

if ! jq -e \
  --arg project_id "${project_id}" \
  --arg region "${region}" \
  --arg network_name "${network_name}" \
  --arg subnet_name "${subnet_name}" '
  def contains_sensitive:
    [.. | select(type == "boolean" and . == true)] | length > 0;

  def allowed_actions:
    . == ["create"] or . == ["no-op"];

  def network_reference_matches($name):
    if type == "string" then
      . == $name or endswith("/global/networks/" + $name)
    else
      false
    end;

  [
    "google_compute_firewall.internal_remote_connection_firewall_ingress",
    "google_compute_network.packer_network",
    "google_compute_subnetwork.packer_subnetwork"
  ] as $expected_addresses
  | [
      .resource_changes[]?
      | select(.mode == "managed")
    ] as $resources
  | (
      $resources
      | map(.address)
      | sort
    ) as $actual_addresses
  | (
      $resources
      | map(
          select(.address == "google_compute_network.packer_network")
        )
      | first
    ) as $network
  | (
      $resources
      | map(
          select(.address == "google_compute_subnetwork.packer_subnetwork")
        )
      | first
    ) as $subnetwork
  | (
      $resources
      | map(
          select(
            .address
            == "google_compute_firewall.internal_remote_connection_firewall_ingress"
          )
        )
      | first
    ) as $firewall
  |
    .format_version == "1.2"
    and .terraform_version == "1.7.5"
    and .errored != true
    and ((.complete // true) == true)
    and ((.resource_drift // []) | length == 0)
    and ((.output_changes // {}) | length == 0)
    and all(.checks[]?; .status == "pass")
    and (
      (.variables | keys | sort)
      == ["gcp_project_id", "gcp_region", "network_name", "subnet_name"]
    )
    and .variables.gcp_project_id.value == $project_id
    and .variables.gcp_region.value == $region
    and .variables.network_name.value == $network_name
    and .variables.subnet_name.value == $subnet_name
    and $actual_addresses == $expected_addresses
    and all($resources[]; .change.actions | allowed_actions)
    and all(
      $resources[];
      ((.change.after_sensitive // {}) | contains_sensitive | not)
    )
    and $network.change.after.project == $project_id
    and $network.change.after.name == $network_name
    and $network.change.after.auto_create_subnetworks == false
    and $network.change.after.delete_default_routes_on_create == false
    and $network.change.after.enable_ula_internal_ipv6 == false
    and $network.change.after.mtu == 1460
    and $network.change.after.network_firewall_policy_enforcement_order
      == "AFTER_CLASSIC_FIREWALL"
    and $network.change.after.routing_mode == "REGIONAL"
    and $subnetwork.change.after.project == $project_id
    and $subnetwork.change.after.region == $region
    and $subnetwork.change.after.name == $subnet_name
    and $subnetwork.change.after.ip_cidr_range == "10.0.0.0/8"
    and $subnetwork.change.after.private_ip_google_access == false
    and $subnetwork.change.after.send_secondary_ip_range_if_empty == false
    and $subnetwork.change.after.stack_type == "IPV4_ONLY"
    and (
      ($subnetwork.change.after.network | network_reference_matches($network_name))
      or (
        $subnetwork.change.after.network == null
        and $subnetwork.change.after_unknown.network == true
      )
    )
    and ($subnetwork.change.after.log_config | length) == 1
    and $subnetwork.change.after.log_config[0].aggregation_interval
      == "INTERVAL_15_MIN"
    and $subnetwork.change.after.log_config[0].flow_sampling == 0
    and $subnetwork.change.after.log_config[0].metadata
      == "EXCLUDE_ALL_METADATA"
    and $firewall.change.after.project == $project_id
    and $firewall.change.after.name
      == ($network_name + "-firewall-ingress")
    and $firewall.change.after.direction == "INGRESS"
    and $firewall.change.after.priority == 900
    and $firewall.change.after.disabled == false
    and (
      $firewall.change.after.destination_ranges == []
      or $firewall.change.after.destination_ranges == null
    )
    and $firewall.change.after.source_ranges == ["35.235.240.0/20"]
    and (
      $firewall.change.after.source_service_accounts == null
      or $firewall.change.after.source_service_accounts == []
    )
    and (
      $firewall.change.after.source_tags == null
      or $firewall.change.after.source_tags == []
    )
    and (
      $firewall.change.after.target_service_accounts == null
      or $firewall.change.after.target_service_accounts == []
    )
    and (
      $firewall.change.after.target_tags == null
      or $firewall.change.after.target_tags == []
    )
    and $firewall.change.after.allow
      == [{"ports": ["22"], "protocol": "tcp"}]
    and (($firewall.change.after.deny // []) | length) == 0
    and (
      ($firewall.change.after.network | network_reference_matches($network_name))
      or (
        $firewall.change.after.network == null
        and $firewall.change.after_unknown.network == true
      )
    )
  ' <<<"${plan_json}" >/dev/null; then
  printf 'Refusing saved network plan: topology or context is not the exact reviewed operator canary.\n' >&2
  jq -c '
    {
      errored,
      complete,
      resource_drift: [
        .resource_drift[]?.address
      ],
      resources: [
        .resource_changes[]?
        | {
            address,
            mode,
            actions: .change.actions
          }
      ],
      variable_names: (.variables | keys)
    }
  ' <<<"${plan_json}" >&2 || true
  exit 1
fi

printf 'Saved network plan matches the exact operator-canary network topology.\n'
