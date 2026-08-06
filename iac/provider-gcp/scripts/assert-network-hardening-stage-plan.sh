#!/usr/bin/env bash
set -euo pipefail

plan_path="${1:?usage: assert-network-hardening-stage-plan.sh PLAN TERRAFORM_BIN STAGE [RECOVERY_STAGE]}"
terraform_bin="${2:-terraform}"
stage="${3:?usage: assert-network-hardening-stage-plan.sh PLAN TERRAFORM_BIN STAGE [RECOVERY_STAGE]}"
recovery_stage="${4:-}"

case "${stage}" in
  network) previous='disabled' ;;
  server) previous='network' ;;
  api) previous='server' ;;
  worker) previous='api' ;;
  build) previous='worker' ;;
  *)
    printf 'Unknown network-hardening rollout stage: %s\n' "${stage}" >&2
    exit 2
    ;;
esac

if [[ -n "${recovery_stage}" && "${recovery_stage}" != "${stage}" ]]; then
  printf 'Network-hardening recovery context must match the reviewed stage: %s != %s\n' \
    "${recovery_stage}" "${stage}" >&2
  exit 2
fi

plan_json="$("${terraform_bin}" show -json "${plan_path}")"
jq -e '.errored != true' <<<"${plan_json}" >/dev/null || {
  printf 'Refusing errored network-hardening plan.\n' >&2
  exit 1
}

guard_address='module.cluster.terraform_data.os_login_operator_access_guard'
case "${stage}" in
  network)
    completion_address='module.cluster.terraform_data.network_hardening_rollout_completion_network'
    marker_address='module.cluster.terraform_data.network_hardening_rollout_stage_network'
    prior_ledger='[]'
    ;;
  server)
    completion_address='module.cluster.terraform_data.network_hardening_rollout_completion_server[0]'
    marker_address='module.cluster.terraform_data.network_hardening_rollout_stage_server[0]'
    prior_ledger='[
      {"address":"module.cluster.terraform_data.network_hardening_rollout_completion_network","input":"network"},
      {"address":"module.cluster.terraform_data.network_hardening_rollout_stage_network","input":"network"}
    ]'
    ;;
  api)
    completion_address='module.cluster.terraform_data.network_hardening_rollout_completion_api[0]'
    marker_address='module.cluster.terraform_data.network_hardening_rollout_stage_api[0]'
    prior_ledger='[
      {"address":"module.cluster.terraform_data.network_hardening_rollout_completion_network","input":"network"},
      {"address":"module.cluster.terraform_data.network_hardening_rollout_stage_network","input":"network"},
      {"address":"module.cluster.terraform_data.network_hardening_rollout_completion_server[0]","input":"server"},
      {"address":"module.cluster.terraform_data.network_hardening_rollout_stage_server[0]","input":"server"}
    ]'
    ;;
  worker)
    completion_address='module.cluster.terraform_data.network_hardening_rollout_completion_worker[0]'
    marker_address='module.cluster.terraform_data.network_hardening_rollout_stage_worker[0]'
    prior_ledger='[
      {"address":"module.cluster.terraform_data.network_hardening_rollout_completion_network","input":"network"},
      {"address":"module.cluster.terraform_data.network_hardening_rollout_stage_network","input":"network"},
      {"address":"module.cluster.terraform_data.network_hardening_rollout_completion_server[0]","input":"server"},
      {"address":"module.cluster.terraform_data.network_hardening_rollout_stage_server[0]","input":"server"},
      {"address":"module.cluster.terraform_data.network_hardening_rollout_completion_api[0]","input":"api"},
      {"address":"module.cluster.terraform_data.network_hardening_rollout_stage_api[0]","input":"api"}
    ]'
    ;;
  build)
    completion_address='module.cluster.terraform_data.network_hardening_rollout_completion_build[0]'
    marker_address='module.cluster.terraform_data.network_hardening_rollout_stage_build[0]'
    prior_ledger='[
      {"address":"module.cluster.terraform_data.network_hardening_rollout_completion_network","input":"network"},
      {"address":"module.cluster.terraform_data.network_hardening_rollout_stage_network","input":"network"},
      {"address":"module.cluster.terraform_data.network_hardening_rollout_completion_server[0]","input":"server"},
      {"address":"module.cluster.terraform_data.network_hardening_rollout_stage_server[0]","input":"server"},
      {"address":"module.cluster.terraform_data.network_hardening_rollout_completion_api[0]","input":"api"},
      {"address":"module.cluster.terraform_data.network_hardening_rollout_stage_api[0]","input":"api"},
      {"address":"module.cluster.terraform_data.network_hardening_rollout_completion_worker[0]","input":"worker"},
      {"address":"module.cluster.terraform_data.network_hardening_rollout_stage_worker[0]","input":"worker"}
    ]'
    ;;
esac

guard_count="$(jq --arg address "${guard_address}" '[.resource_changes[]? | select(.address == $address)] | length' <<<"${plan_json}")"
[[ "${guard_count}" -eq 1 ]] || {
  printf 'OS Login authorization guard must be present exactly once in the targeted cluster graph.\n' >&2
  exit 1
}
jq -e --arg address "${guard_address}" '
  .resource_changes[]
  | select(.address == $address)
  | .change.after.input == true
    and (
      .change.actions == ["create"]
      or .change.actions == ["no-op"]
      or .change.actions == ["update"]
    )
' <<<"${plan_json}" >/dev/null || {
  printf 'OS Login authorization guard is not explicitly open in the reviewed plan.\n' >&2
  exit 1
}

completion_count="$(jq --arg address "${completion_address}" '[.resource_changes[]? | select(.address == $address)] | length' <<<"${plan_json}")"
[[ "${completion_count}" -eq 1 ]] || {
  printf 'Network-hardening convergence sentinel must be present exactly once.\n' >&2
  exit 1
}
jq -e \
  --arg address "${completion_address}" \
  --arg stage "${stage}" '
    .resource_changes[]
    | select(.address == $address)
    | .change.after.input == $stage
    and (
      (
        .change.before == null
        and .change.actions == ["create"]
      )
      or (
        .change.before.input == $stage
        and (
          .change.actions == ["delete", "create"]
          or .change.actions == ["create", "delete"]
        )
      )
      or (
        $stage == "network"
        and .change.before.input == "disabled"
        and (
          .change.actions == ["delete", "create"]
          or .change.actions == ["create", "delete"]
        )
      )
    )
  ' <<<"${plan_json}" >/dev/null || {
  printf 'Network-hardening convergence sentinel is not a valid initial or retry transition for %s -> %s.\n' \
    "${previous}" "${stage}" >&2
  exit 1
}

marker_count="$(jq --arg address "${marker_address}" '[.resource_changes[]? | select(.address == $address)] | length' <<<"${plan_json}")"
[[ "${marker_count}" -eq 1 ]] || {
  printf 'Network-hardening state marker must be present exactly once.\n' >&2
  exit 1
}
jq -e \
  --arg address "${marker_address}" \
  --arg stage "${stage}" \
  --arg recovery_stage "${recovery_stage}" '
    .resource_changes[]
    | select(.address == $address)
    | .change.after.input == $stage
    and (
      (
        $stage == "network"
        and (.change.before == null or .change.before.input == "disabled")
        and (.change.actions == ["create"] or .change.actions == ["update"])
      )
      or (
        $stage != "network"
        and .change.before == null
        and .change.actions == ["create"]
      )
      or (
        $recovery_stage == $stage
        and .change.before.input == $stage
        and .change.actions == ["no-op"]
      )
    )
  ' <<<"${plan_json}" >/dev/null || {
  printf 'Network-hardening stage must advance exactly %s -> %s or remain a no-op during a forced-convergence retry.\n' \
    "${previous}" "${stage}" >&2
  exit 1
}

jq -e --argjson expected "${prior_ledger}" '
  [
    $expected[] as $want
    | [.resource_changes[]? | select(.address == $want.address)] as $matches
    | ($matches | length) == 1
      and $matches[0].change.actions == ["no-op"]
      and $matches[0].change.before.input == $want.input
      and $matches[0].change.after.input == $want.input
  ]
  | all
' <<<"${plan_json}" >/dev/null || {
  printf 'Network-hardening %s stage is missing a clean cumulative prior-stage ledger.\n' \
    "${stage}" >&2
  exit 1
}

case "${stage}" in
  network)
    expected_mutations='[
      "module.cluster.module.network.google_compute_firewall.iap_remote_connection_firewall_ingress[0]",
      "module.cluster.module.network.google_compute_firewall.internal_remote_connection_firewall_ingress",
      "module.cluster.module.network.google_compute_firewall.remote_connection_firewall_ingress"
    ]'
    ;;
  server)
    expected_mutations='[
      "module.cluster.google_storage_bucket_object.setup_config_objects[\"scripts/run-nomad.sh\"]",
      "module.cluster.google_compute_health_check.server_nomad_check",
      "module.cluster.google_compute_instance_template.server",
      "module.cluster.google_compute_region_instance_group_manager.server_pool"
    ]'
    ;;
  api)
    expected_mutations='[
      "module.cluster.google_compute_instance_group_manager.api_pool",
      "module.cluster.google_compute_instance_template.api"
    ]'
    ;;
  worker)
    expected_mutations='[
      "module.cluster.module.client_cluster[\"default\"].google_compute_instance_template.template",
      "module.cluster.module.client_cluster[\"default\"].google_compute_region_instance_group_manager.pool"
    ]'
    ;;
  build)
    expected_mutations='[
      "module.cluster.google_compute_instance_group_manager.clickhouse_pool",
      "module.cluster.google_compute_instance_group_manager.loki_pool",
      "module.cluster.google_compute_instance_template.clickhouse",
      "module.cluster.google_compute_instance_template.loki",
      "module.cluster.module.build_cluster[\"default\"].google_compute_instance_template.template",
      "module.cluster.module.build_cluster[\"default\"].google_compute_region_instance_group_manager.pool"
    ]'
    ;;
esac

actual_mutations="$(jq -cS \
  --arg guard "${guard_address}" \
  --arg completion "${completion_address}" \
  --arg marker "${marker_address}" '
    [
      .resource_changes[]?
      | select(.mode == "managed")
      | select(.change.actions != ["no-op"] and .change.actions != ["read"])
      | select(.address != $guard and .address != $completion and .address != $marker)
      | .address
    ]
    | sort
  ' <<<"${plan_json}")"
expected_mutations="$(jq -cS 'sort' <<<"${expected_mutations}")"
unexpected_mutations="$(jq -cn \
  --argjson actual "${actual_mutations}" \
  --argjson allowed "${expected_mutations}" \
  '$actual - $allowed')"
if [[ "$(jq 'length' <<<"${unexpected_mutations}")" -ne 0 ]]; then
  printf 'Refusing %s stage: mutation set escapes its exact reviewed pool boundary.\n' \
    "${stage}" >&2
  printf 'Allowed: %s\nActual:  %s\n' "${expected_mutations}" "${actual_mutations}" >&2
  exit 1
fi

if [[ "${stage}" == "server" ]]; then
  jq -e '
    [
      .resource_changes[]?
      | select(
          .address
          == "module.cluster.google_storage_bucket_object.setup_config_objects[\"scripts/run-nomad.sh\"]"
        )
    ] as $matches
    | ($matches | length) == 1
      and $matches[0].mode == "managed"
      and $matches[0].type == "google_storage_bucket_object"
      and (
        $matches[0].change.actions == ["no-op"]
        or (
          (
            $matches[0].change.actions == ["create"]
            or
            $matches[0].change.actions == ["create", "delete"]
            or $matches[0].change.actions == ["delete", "create"]
          )
          and (
            (
              $matches[0].change.actions == ["create"]
              and $matches[0].change.before == null
            )
            or ($matches[0].change.before.name | test("^run-nomad-[0-9a-f]{5}\\.sh$"))
          )
          and ($matches[0].change.after.name | test("^run-nomad-[0-9a-f]{5}\\.sh$"))
          and ($matches[0].change.after.source | endswith("nomad-cluster/scripts/run-nomad.sh"))
        )
      )
  ' <<<"${plan_json}" >/dev/null || {
    printf 'Refusing server stage: the exact restart-safe Nomad bootstrap object is missing or unsafe.\n' >&2
    exit 1
  }

  jq -e '
    def one($address):
      [.resource_changes[]? | select(.address == $address)]
      | if length == 1 then .[0] else null end;
    def field_unknown($resource; $field):
      any(
        ($resource.change.after_unknown // {} | .. | objects);
        has($field) and .[$field] == true
      );
    def container_unknown($resource; $container):
      any(
        ($resource.change.after_unknown[$container] // false | ..);
        . == true
      );
    def normalize_compute_resource_id:
      if type == "string" then
        sub("^https://www.googleapis.com/compute/(v1|beta)/"; "")
        | sub("^https://compute.googleapis.com/compute/(v1|beta)/"; "")
        | sub("^//compute.googleapis.com/"; "")
      else
        .
      end;
    one("module.cluster.google_compute_health_check.server_nomad_check") as $health
    | one("module.cluster.google_compute_region_instance_group_manager.server_pool") as $server
    | $health != null
      and $health.type == "google_compute_health_check"
      and ($health.change.actions | index("delete") | not)
      and (field_unknown($health; "id") | not)
      and ($health.change.after.id | type) == "string"
      and (
        $health.change.after.id
        | normalize_compute_resource_id
        | test("^projects/[^/]+/global/healthChecks/[^/]+$")
      )
      and (field_unknown($health; "check_interval_sec") | not)
      and (field_unknown($health; "timeout_sec") | not)
      and (field_unknown($health; "healthy_threshold") | not)
      and (field_unknown($health; "unhealthy_threshold") | not)
      and (container_unknown($health; "http_health_check") | not)
      and $health.change.after.check_interval_sec == 5
      and $health.change.after.timeout_sec == 5
      and $health.change.after.healthy_threshold == 2
      and $health.change.after.unhealthy_threshold == 10
      and ($health.change.after.http_health_check | length) == 1
      and $health.change.after.http_health_check[0].port == 50001
      and $health.change.after.http_health_check[0].request_path == "/healthz"
      and $server != null
      and ($server.change.actions | index("delete") | not)
      and ($server.change.after.distribution_policy_zones | type) == "array"
      and (field_unknown($server; "distribution_policy_zones") | not)
      and ($server.change.after.distribution_policy_zones | length) >= 1
      and ($server.change.after.update_policy | length) == 1
      and (container_unknown($server; "update_policy") | not)
      and $server.change.after.update_policy[0].replacement_method == "SUBSTITUTE"
      and $server.change.after.update_policy[0].max_unavailable_fixed == 0
      and ($server.change.after.update_policy[0].max_unavailable_percent // 0) == 0
      and $server.change.after.update_policy[0].max_surge_fixed
        >= ($server.change.after.distribution_policy_zones | length)
      and ($server.change.after.auto_healing_policies | length) == 1
      and (container_unknown($server; "auto_healing_policies") | not)
      and ($server.change.after.auto_healing_policies[0].health_check | type) == "string"
      and (
        $server.change.after.auto_healing_policies[0].health_check
        | normalize_compute_resource_id
      ) == ($health.change.after.id | normalize_compute_resource_id)
      and $server.change.after.auto_healing_policies[0].initial_delay_sec == 120
      and ($server.change.after.instance_lifecycle_policy | length) == 1
      and (container_unknown($server; "instance_lifecycle_policy") | not)
      and $server.change.after.instance_lifecycle_policy[0].default_action_on_failure == "REPAIR"
      and $server.change.after.instance_lifecycle_policy[0].force_update_on_repair == "NO"
      and $server.change.after.instance_lifecycle_policy[0].on_failed_health_check == "DO_NOTHING"
  ' <<<"${plan_json}" >/dev/null || {
    printf 'Refusing server stage: local-voter health, substitute rollout, or zone-surge invariants are unsafe.\n' >&2
    exit 1
  }
fi

if [[ "${stage}" == "network" ]]; then
  jq -e '
    def ports($rule):
      [$rule[]? | select(.protocol == "tcp") | .ports[]?] | sort;
    def logged:
      (.log_config | length) == 1
      and .log_config[0].metadata == "EXCLUDE_ALL_METADATA";
    def after($address):
      [.resource_changes[]? | select(.address == $address)]
      | if length == 1 then .[0].change.after else null end;
    after("module.cluster.module.network.google_compute_firewall.iap_remote_connection_firewall_ingress[0]") as $iap
    | after("module.cluster.module.network.google_compute_firewall.remote_connection_firewall_ingress") as $deny
    | after("module.cluster.module.network.google_compute_firewall.internal_remote_connection_firewall_ingress") as $legacy
    | $iap != null
      and $iap.direction == "INGRESS"
      and $iap.priority == 700
      and ($iap.source_ranges | sort) == ["35.235.240.0/20"]
      and ($iap.target_tags | sort) == ["orch"]
      and ports($iap.allow) == ["22", "3389"]
      and (($iap.deny // []) | length) == 0
      and ($iap | logged)
      and $deny != null
      and $deny.direction == "INGRESS"
      and $deny.priority == 800
      and ($deny.source_ranges | sort) == ["0.0.0.0/0"]
      and ($deny.target_tags | sort) == ["orch"]
      and ports($deny.deny) == ["22", "3389"]
      and (($deny.allow // []) | length) == 0
      and ($deny | logged)
      and $legacy != null
      and $legacy.direction == "INGRESS"
      and $legacy.priority == 900
      and ($legacy.source_ranges | sort) == ["0.0.0.0/0", "35.235.240.0/20"]
      and ($legacy.target_tags | sort) == ["orch"]
      and ports($legacy.allow) == ["22", "3389"]
      and (($legacy.deny // []) | length) == 0
      and ($legacy | logged)
  ' <<<"${plan_json}" >/dev/null || {
    printf 'Refusing network stage: firewall precedence or exact rule semantics are unsafe.\n' >&2
    exit 1
  }
fi

# A failed apply can already have committed a template or MIG update while the
# downstream convergence sentinel and stage marker remain at the previous
# stage. Accept any mutation subset inside this stage's boundary so that the
# same reviewed stage can be retried, while still rejecting rollback, skips,
# later-pool changes, and generic-autoscaler ownership changes.

# The cumulative dependency chain pulls every completed template into the
# exact-stage plan while keeping future pools out. Re-prove OS Login intent for
# all prior/current templates and reject any mutation outside the current pool.
template_expectations="$(jq -cn --arg stage "${stage}" '
  if $stage == "network" then []
  elif $stage == "server" then [
    {address:"module.cluster.google_compute_instance_template.server"}
  ]
  elif $stage == "api" then [
    {address:"module.cluster.google_compute_instance_template.server"},
    {address:"module.cluster.google_compute_instance_template.api"}
  ]
  elif $stage == "worker" then [
    {address:"module.cluster.google_compute_instance_template.server"},
    {address:"module.cluster.google_compute_instance_template.api"},
    {address:"module.cluster.module.client_cluster[\"default\"].google_compute_instance_template.template"}
  ]
  else [
    {address:"module.cluster.google_compute_instance_template.server"},
    {address:"module.cluster.google_compute_instance_template.api"},
    {address:"module.cluster.module.client_cluster[\"default\"].google_compute_instance_template.template"},
    {address:"module.cluster.module.build_cluster[\"default\"].google_compute_instance_template.template"},
    {address:"module.cluster.google_compute_instance_template.loki"},
    {address:"module.cluster.google_compute_instance_template.clickhouse"}
  ]
  end
')"
jq -e --argjson expected "${template_expectations}" '
  [
    $expected[] as $want
    | [ .resource_changes[]? | select(.address == $want.address) ] as $matches
    | ($matches | length) == 1
      and $matches[0].change.after.metadata["enable-oslogin"] == "TRUE"
  ]
  | all
' <<<"${plan_json}" >/dev/null || {
  printf 'Refusing %s stage: cumulative OS Login template intent is incomplete.\n' \
    "${stage}" >&2
  exit 1
}

printf 'Network-hardening stage plan passed: %s (%s -> %s).\n' \
  "${stage}" "${previous}" "${stage}"
