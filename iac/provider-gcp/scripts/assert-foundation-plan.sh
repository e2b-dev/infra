#!/usr/bin/env bash
set -euo pipefail

plan_path="${1:?usage: assert-foundation-plan.sh PLAN_PATH [TERRAFORM_BIN] [apply|destroy] [PROJECT REGION ENVIRONMENT]}"
terraform_bin="${2:-terraform}"
mode="${3:-apply}"
expected_project="${4:-}"
expected_region="${5:-}"
expected_environment="${6:-}"

if [[ "${mode}" != "apply" && "${mode}" != "destroy" ]]; then
  printf 'Unsupported foundation plan inspection mode: %s\n' "${mode}" >&2
  exit 2
fi

command -v jq >/dev/null 2>&1 || {
  printf 'jq is required to inspect the saved foundation plan.\n' >&2
  exit 1
}

plan_json="$("${terraform_bin}" show -json "${plan_path}")"

changed_addresses="$(
  jq -r '
    .resource_changes[]?
    | select(.change.actions != ["no-op"])
    | .address
  ' <<<"${plan_json}"
)"
unexpected_addresses="$(
  printf '%s\n' "${changed_addresses}" \
    | sed '/^[[:space:]]*$/d' \
    | grep -Ev '^module\.init\.' \
    || true
)"
if [[ "${mode}" == "apply" ]]; then
  disallowed_changes="$(
    jq -r '
      .resource_changes[]?
      | select(.change.actions | index("delete"))
      | "\(.address): \(.change.actions | join(","))"
    ' <<<"${plan_json}"
  )"
else
  disallowed_changes="$(
    jq -r '
      .resource_changes[]?
      | select(
          .change.actions != ["no-op"]
          and .change.actions != ["delete"]
          and (.mode != "data" or .change.actions != ["read"])
        )
      | "\(.address): \(.change.actions | join(","))"
    ' <<<"${plan_json}"
  )"
fi
forbidden_credentials="$(
  jq -r '
    .resource_changes[]?
    | select(
        .type == "google_service_account_key"
        or .type == "google_storage_hmac_key"
      )
    | .address
  ' <<<"${plan_json}"
)"
identity_mismatches="$(
  jq -r \
    --arg expected_project "${expected_project}" \
    --arg expected_region "${expected_region}" \
    --arg expected_environment "${expected_environment}" \
    '
      [
        if $expected_project != ""
          and (.variables.gcp_project_id.value? != $expected_project)
        then "gcp_project_id: expected=\($expected_project) actual=\(.variables.gcp_project_id.value? // "<missing>")"
        else empty end,
        if $expected_region != ""
          and (.variables.gcp_region.value? != $expected_region)
        then "gcp_region: expected=\($expected_region) actual=\(.variables.gcp_region.value? // "<missing>")"
        else empty end,
        if $expected_environment != ""
          and (.variables.environment.value? != $expected_environment)
        then "environment: expected=\($expected_environment) actual=\(.variables.environment.value? // "<missing>")"
        else empty end
      ]
      | .[]
    ' <<<"${plan_json}"
)"

if [[ -n "${unexpected_addresses}" \
  || -n "${disallowed_changes}" \
  || -n "${forbidden_credentials}" \
  || -n "${identity_mismatches}" ]]; then
  printf 'Refusing foundation plan: review allowlist failed.\n' >&2
  if [[ -n "${unexpected_addresses}" ]]; then
    printf 'Changed addresses outside module.init:\n%s\n' "${unexpected_addresses}" >&2
  fi
  if [[ -n "${disallowed_changes}" ]]; then
    printf 'Changes are incompatible with %s inspection mode:\n%s\n' "${mode}" "${disallowed_changes}" >&2
  fi
  if [[ -n "${forbidden_credentials}" ]]; then
    printf 'Forbidden long-lived credential resources:\n%s\n' "${forbidden_credentials}" >&2
  fi
  if [[ -n "${identity_mismatches}" ]]; then
    printf 'Plan identity does not match the selected foundation context:\n%s\n' \
      "${identity_mismatches}" >&2
  fi
  exit 1
fi

printf 'Foundation plan allowlist passed: %s changed module.init addresses.\n' \
  "$(printf '%s\n' "${changed_addresses}" | sed '/^[[:space:]]*$/d' | wc -l | tr -d ' ')"
