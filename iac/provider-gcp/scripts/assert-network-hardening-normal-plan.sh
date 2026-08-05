#!/usr/bin/env bash
set -euo pipefail

plan_path="${1:?usage: assert-network-hardening-normal-plan.sh PLAN TERRAFORM_BIN ENVIRONMENT}"
terraform_bin="${2:-terraform}"
expected_environment="${3:?usage: assert-network-hardening-normal-plan.sh PLAN TERRAFORM_BIN ENVIRONMENT}"

[[ "${expected_environment}" =~ ^[A-Za-z0-9][A-Za-z0-9._-]*$ ]] || {
  printf 'Invalid expected environment for ordinary workload plan: %s\n' \
    "${expected_environment}" >&2
  exit 1
}

plan_json="$(${terraform_bin} show -json "${plan_path}")"
jq -e '.errored != true' <<<"${plan_json}" >/dev/null || {
  printf 'Refusing errored workload plan.\n' >&2
  exit 1
}

completion_address='module.cluster.terraform_data.network_hardening_rollout_completion'
marker_address='module.cluster.terraform_data.network_hardening_rollout_stage'

assert_stable_stage_resource() {
  local address="$1"
  local label="$2"
  local count

  count="$(jq --arg address "${address}" \
    '[.resource_changes[]? | select(.address == $address)] | length' \
    <<<"${plan_json}")"
  [[ "${count}" -eq 1 ]] || {
    printf '%s must be present exactly once in an ordinary workload plan.\n' \
      "${label}" >&2
    exit 1
  }

  if [[ "${expected_environment}" == "dev" ]]; then
    jq -e --arg address "${address}" '
      .resource_changes[]
      | select(.address == $address)
      | .mode == "managed"
        and .type == "terraform_data"
        and .change.actions == ["no-op"]
        and .change.before.input == .change.after.input
        and (
          .change.after.input == "network"
          or .change.after.input == "server"
          or .change.after.input == "api"
          or .change.after.input == "worker"
          or .change.after.input == "build"
        )
    ' <<<"${plan_json}" >/dev/null || {
      printf '%s may not initialize, advance, regress, or remain disabled in an ordinary dev workload plan; use the reviewed staged cluster workflow.\n' \
        "${label}" >&2
      exit 1
    }
  else
    jq -e --arg address "${address}" '
      .resource_changes[]
      | select(.address == $address)
      | .mode == "managed"
        and .type == "terraform_data"
        and .change.after.input == "disabled"
        and (
          (
            .change.actions == ["no-op"]
            and .change.before.input == "disabled"
          )
          or (
            .change.actions == ["create"]
            and .change.before == null
          )
        )
    ' <<<"${plan_json}" >/dev/null || {
      printf '%s in non-dev environment %s may only initialize or remain stable at disabled in an ordinary workload plan.\n' \
        "${label}" "${expected_environment}" >&2
      exit 1
    }
  fi
}

assert_stable_stage_resource \
  "${completion_address}" 'Network-hardening convergence sentinel'
assert_stable_stage_resource \
  "${marker_address}" 'Network-hardening state marker'

completion_stage="$(jq -r --arg address "${completion_address}" '
  .resource_changes[] | select(.address == $address) | .change.after.input
' <<<"${plan_json}")"
marker_stage="$(jq -r --arg address "${marker_address}" '
  .resource_changes[] | select(.address == $address) | .change.after.input
' <<<"${plan_json}")"

[[ "${completion_stage}" == "${marker_stage}" ]] || {
  printf 'Network-hardening convergence sentinel and state marker disagree (%s != %s).\n' \
    "${completion_stage}" "${marker_stage}" >&2
  exit 1
}

# The stage resources alone are not sufficient if a future template change
# accidentally decouples OS Login from the rollout variable. Prove cumulative
# intent on every managed fleet template while allowing unrelated reviewed
# template replacements (for example, an image revision) to proceed.
template_expectations="$(jq -cn --arg stage "${marker_stage}" '
  {disabled: 0, network: 1, server: 2, api: 3, worker: 4, build: 5} as $rank
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
        else (
          ($matches[0].change.after.metadata // {})
          | has("enable-oslogin")
          | not
        )
        end
      )
  ]
  | all
' <<<"${plan_json}" >/dev/null || {
  printf 'Ordinary workload plan has OS Login template intent inconsistent with completed stage %s.\n' \
    "${marker_stage}" >&2
  exit 1
}

if [[ "${expected_environment}" == "dev" ]]; then
  printf 'Ordinary dev workload plan preserves completed network-hardening stage: %s.\n' \
    "${marker_stage}"
else
  printf 'Ordinary non-dev workload plan preserves network-hardening stage disabled: %s.\n' \
    "${expected_environment}"
fi
