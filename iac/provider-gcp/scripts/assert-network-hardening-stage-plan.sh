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
completion_address='module.cluster.terraform_data.network_hardening_rollout_completion'
marker_address='module.cluster.terraform_data.network_hardening_rollout_stage'

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
  --arg stage "${stage}" \
  --arg previous "${previous}" '
    .resource_changes[]
    | select(.address == $address)
    | .change.after.input == $stage
    and (
      (
        .change.before == null
        and .change.actions == ["create"]
      )
      or (
        .change.before.input == $previous
        and (
          .change.actions == ["create"]
          or .change.actions == ["delete", "create"]
          or .change.actions == ["create", "delete"]
        )
      )
      or (
        .change.before.input == $stage
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
  --arg completion "${completion_address}" \
  --arg stage "${stage}" \
  --arg previous "${previous}" \
  --arg recovery_stage "${recovery_stage}" '
    . as $plan
    | ($plan.resource_changes[] | select(.address == $completion)) as $completion_change
    | $plan.resource_changes[]
    | select(.address == $address)
    | .change.after.input == $stage
    and (
      (
        (
          if $stage == "network"
          then (.change.before == null or .change.before.input == $previous)
          else .change.before.input == $previous
          end
        )
        and (
          .change.actions == ["create"]
          or .change.actions == ["update"]
        )
        and (
          (
            $completion_change.change.before == null
            and $completion_change.change.actions == ["create"]
            and (
              $stage == "network"
              or $recovery_stage == $stage
            )
          )
          or (
            (
              $completion_change.change.before.input == $previous
              or $completion_change.change.before.input == $stage
            )
            and (
              $completion_change.change.actions == ["create"]
              or $completion_change.change.actions == ["delete", "create"]
              or $completion_change.change.actions == ["create", "delete"]
            )
          )
        )
      )
      or (
        .change.before.input == $stage
        and .change.actions == ["no-op"]
        and (
          (
            $completion_change.change.before == null
            and $completion_change.change.actions == ["create"]
            and $recovery_stage == $stage
          )
          or (
            $completion_change.change.before.input == $stage
            and (
              $completion_change.change.actions == ["delete", "create"]
              or $completion_change.change.actions == ["create", "delete"]
            )
          )
        )
      )
    )
  ' <<<"${plan_json}" >/dev/null || {
  printf 'Network-hardening stage must advance exactly %s -> %s or remain a no-op during a forced-convergence retry.\n' \
    "${previous}" "${stage}" >&2
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

# Enforce cumulative OS Login intent across every managed template. This also
# proves that a stage cannot accidentally roll a later pool.
template_expectations="$(jq -cn --arg stage "${stage}" '
  {network: 1, server: 2, api: 3, worker: 4, build: 5} as $rank
  | ($rank[$stage]) as $current
  | [
      {address:"module.cluster.google_compute_instance_template.server", enabled:($current >= 2)},
      {address:"module.cluster.google_compute_instance_template.api", enabled:($current >= 3)},
      {address:"module.cluster.module.client_cluster[\"default\"].google_compute_instance_template.template", enabled:($current >= 4)},
      {address:"module.cluster.module.build_cluster[\"default\"].google_compute_instance_template.template", enabled:($current >= 5)},
      {address:"module.cluster.google_compute_instance_template.loki", enabled:($current >= 5)},
      {address:"module.cluster.google_compute_instance_template.clickhouse", enabled:($current >= 5)}
    ]
')"
jq -e --argjson expected "${template_expectations}" '
  [
    $expected[] as $want
    | [ .resource_changes[]? | select(.address == $want.address) ] as $matches
    | ($matches | length) == 1
      and (
        if $want.enabled
        then $matches[0].change.after.metadata["enable-oslogin"] == "TRUE"
        else ($matches[0].change.after.metadata | has("enable-oslogin") | not)
        end
      )
  ]
  | all
' <<<"${plan_json}" >/dev/null || {
  printf 'Refusing %s stage: cumulative OS Login template intent is incomplete.\n' \
    "${stage}" >&2
  exit 1
}

printf 'Network-hardening stage plan passed: %s (%s -> %s).\n' \
  "${stage}" "${previous}" "${stage}"
