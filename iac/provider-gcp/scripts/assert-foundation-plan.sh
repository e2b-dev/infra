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

umask 077
inspection_dir="$(mktemp -d)"
plan_json_path="${inspection_dir}/plan.json"
plan_text_path="${inspection_dir}/plan.txt"
trap 'rm -rf -- "${inspection_dir}"' EXIT

"${terraform_bin}" show -json "${plan_path}" >"${plan_json_path}"
"${terraform_bin}" show -no-color "${plan_path}" >"${plan_text_path}"

changed_addresses="$(
  jq -r '
    .resource_changes[]?
    | select(.change.actions != ["no-op"])
    | .address
  ' "${plan_json_path}"
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
    ' "${plan_json_path}"
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
    ' "${plan_json_path}"
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
  ' "${plan_json_path}"
)"
forbidden_acl_generators="$(
  jq -r '
    .resource_changes[]?
    | select(
        .type == "random_uuid"
        and (.name == "consul_acl_token" or .name == "nomad_acl_token")
      )
    | .address
  ' "${plan_json_path}"
)"
acl_sensitivity_failures="$(
  jq -r '
    .resource_changes[]?
    | select(
        (
          .type == "random_password"
          and (.name == "consul_acl_token_seed" or .name == "nomad_acl_token_seed")
          and .change.after_sensitive.result != true
        )
        or
        (
          .type == "google_secret_manager_secret_version"
          and (.name == "consul_acl_token_active" or .name == "nomad_acl_token_active")
          and .change.after_sensitive.secret_data != true
        )
      )
    | "\(.address): active ACL material is not marked sensitive"
  ' "${plan_json_path}"
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
    ' "${plan_json_path}"
)"

acl_material_visible=""
while IFS= read -r active_value; do
  [[ -n "${active_value}" ]] || continue
  if grep -Fq -- "${active_value}" "${plan_text_path}"; then
    acl_material_visible="active ACL material appeared in human-readable plan output"
    break
  fi
done < <(
  jq -r '
    .resource_changes[]?
    | select(
        (
          .type == "random_password"
          and (.name == "consul_acl_token_seed" or .name == "nomad_acl_token_seed")
        )
        or
        (
          .type == "google_secret_manager_secret_version"
          and (.name == "consul_acl_token_active" or .name == "nomad_acl_token_active")
        )
      )
    | if .type == "random_password"
      then [.change.before.result?, .change.after.result?]
      else [.change.before.secret_data?, .change.after.secret_data?]
      end
    | .[]
    | select(type == "string" and length > 0)
  ' "${plan_json_path}"
)

if [[ -n "${unexpected_addresses}" \
  || -n "${disallowed_changes}" \
  || -n "${forbidden_credentials}" \
  || -n "${forbidden_acl_generators}" \
  || -n "${acl_sensitivity_failures}" \
  || -n "${acl_material_visible}" \
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
  if [[ -n "${forbidden_acl_generators}" ]]; then
    printf 'Forbidden non-sensitive ACL token generators:\n%s\n' "${forbidden_acl_generators}" >&2
  fi
  if [[ -n "${acl_sensitivity_failures}" ]]; then
    printf 'ACL sensitivity checks failed:\n%s\n' "${acl_sensitivity_failures}" >&2
  fi
  if [[ -n "${acl_material_visible}" ]]; then
    printf 'ACL redaction check failed: %s.\n' "${acl_material_visible}" >&2
  fi
  if [[ -n "${identity_mismatches}" ]]; then
    printf 'Plan identity does not match the selected foundation context:\n%s\n' \
      "${identity_mismatches}" >&2
  fi
  exit 1
fi

printf 'Foundation plan allowlist passed: %s changed module.init addresses.\n' \
  "$(printf '%s\n' "${changed_addresses}" | sed '/^[[:space:]]*$/d' | wc -l | tr -d ' ')"
