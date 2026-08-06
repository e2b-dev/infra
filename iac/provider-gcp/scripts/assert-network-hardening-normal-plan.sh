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

stage_ledger='[
  {
    "stage":"network",
    "completion":"module.cluster.terraform_data.network_hardening_rollout_completion_network",
    "marker":"module.cluster.terraform_data.network_hardening_rollout_stage_network"
  },
  {
    "stage":"server",
    "completion":"module.cluster.terraform_data.network_hardening_rollout_completion_server[0]",
    "marker":"module.cluster.terraform_data.network_hardening_rollout_stage_server[0]"
  },
  {
    "stage":"api",
    "completion":"module.cluster.terraform_data.network_hardening_rollout_completion_api[0]",
    "marker":"module.cluster.terraform_data.network_hardening_rollout_stage_api[0]"
  },
  {
    "stage":"worker",
    "completion":"module.cluster.terraform_data.network_hardening_rollout_completion_worker[0]",
    "marker":"module.cluster.terraform_data.network_hardening_rollout_stage_worker[0]"
  },
  {
    "stage":"build",
    "completion":"module.cluster.terraform_data.network_hardening_rollout_completion_build[0]",
    "marker":"module.cluster.terraform_data.network_hardening_rollout_stage_build[0]"
  }
]'

ledger_addresses="$(jq -cS '
  [
    .resource_changes[]?
    | select(
        .address
        | startswith("module.cluster.terraform_data.network_hardening_rollout_completion")
          or startswith("module.cluster.terraform_data.network_hardening_rollout_stage")
      )
    | .address
  ]
  | sort
' <<<"${plan_json}")"

marker_stage=''
if [[ "${expected_environment}" == "dev" ]]; then
  for candidate_stage in network server api worker build; do
    expected_addresses="$(jq -cnS \
      --argjson ledger "${stage_ledger}" \
      --arg candidate "${candidate_stage}" '
        ($ledger | map(.stage) | index($candidate)) as $candidate_index
        | [
            $ledger[:($candidate_index + 1)][]
            | .completion, .marker
          ]
        | sort
      ')"
    [[ "${ledger_addresses}" == "${expected_addresses}" ]] || continue

    if jq -e \
      --argjson ledger "${stage_ledger}" \
      --arg candidate "${candidate_stage}" '
        ($ledger | map(.stage) | index($candidate)) as $candidate_index
        | [
            $ledger[:($candidate_index + 1)][] as $want
            | [$want.completion, $want.marker][] as $address
            | [.resource_changes[]? | select(.address == $address)] as $matches
            | ($matches | length) == 1
              and $matches[0].mode == "managed"
              and $matches[0].type == "terraform_data"
              and $matches[0].change.actions == ["no-op"]
              and $matches[0].change.before.input == $want.stage
              and $matches[0].change.after.input == $want.stage
          ]
        | all
      ' <<<"${plan_json}" >/dev/null; then
      marker_stage="${candidate_stage}"
      break
    fi
  done

  [[ -n "${marker_stage}" ]] || {
    printf 'Ordinary dev workload plan must preserve one exact no-op cumulative network-hardening ledger; use the reviewed staged cluster workflow.\n' >&2
    exit 1
  }
else
  expected_addresses="$(jq -cnS --argjson ledger "${stage_ledger}" '
    [$ledger[0].completion, $ledger[0].marker] | sort
  ')"
  [[ "${ledger_addresses}" == "${expected_addresses}" ]] || {
    printf 'Ordinary non-dev workload plan must contain only the disabled network-hardening ledger root: %s.\n' \
      "${expected_environment}" >&2
    exit 1
  }
  jq -e --argjson ledger "${stage_ledger}" '
    [
      $ledger[0] as $want
      | [$want.completion, $want.marker][] as $address
      | [.resource_changes[]? | select(.address == $address)] as $matches
      | ($matches | length) == 1
        and $matches[0].mode == "managed"
        and $matches[0].type == "terraform_data"
        and $matches[0].change.after.input == "disabled"
        and (
          (
            $matches[0].change.actions == ["no-op"]
            and $matches[0].change.before.input == "disabled"
          )
          or (
            $matches[0].change.actions == ["create"]
            and $matches[0].change.before == null
          )
        )
    ]
    | all
  ' <<<"${plan_json}" >/dev/null || {
    printf 'Ordinary non-dev workload plan may only initialize or preserve the disabled network-hardening ledger root: %s.\n' \
      "${expected_environment}" >&2
    exit 1
  }
  marker_stage='disabled'
fi

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
