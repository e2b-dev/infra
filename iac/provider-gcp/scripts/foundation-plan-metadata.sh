#!/usr/bin/env bash
set -euo pipefail

mode="${1:-}"

usage() {
  cat >&2 <<'EOF'
usage:
  foundation-plan-metadata.sh fingerprint TERRAFORM_BIN CONFIG_ROOT REPO_ROOT
  foundation-plan-metadata.sh write PLAN MANIFEST TERRAFORM_BIN CONFIG_ROOT REPO_ROOT EXPECTED_FINGERPRINT
  foundation-plan-metadata.sh verify PLAN MANIFEST TERRAFORM_BIN CONFIG_ROOT REPO_ROOT

Required environment:
  FOUNDATION_ENV
  FOUNDATION_ENV_FILE
  FOUNDATION_TF_VAR_FILE
  FOUNDATION_GCP_PROJECT_ID
  FOUNDATION_GCP_REGION
  FOUNDATION_STATE_BUCKET
  FOUNDATION_STATE_PREFIX
EOF
  exit 2
}

sha256_file() {
  shasum -a 256 "$1" | awk '{print $1}'
}

sha256_stdin() {
  shasum -a 256 | awk '{print $1}'
}

assert_private_regular_file() {
  local path="$1"
  local label="$2"
  local mode_bits

  if [[ ! -f "${path}" || -L "${path}" ]]; then
    printf '%s must be a regular, non-symlink file: %s\n' "${label}" "${path}" >&2
    exit 1
  fi

  if mode_bits="$(stat -f '%Lp' "${path}" 2>/dev/null)"; then
    :
  else
    mode_bits="$(stat -c '%a' "${path}")"
  fi
  if (( (8#${mode_bits} & 077) != 0 )); then
    printf '%s must not be readable or writable by group/other: %s (mode %s)\n' \
      "${label}" "${path}" "${mode_bits}" >&2
    exit 1
  fi
}

require_context() {
  local variable_name
  for variable_name in \
    FOUNDATION_ENV \
    FOUNDATION_ENV_FILE \
    FOUNDATION_TF_VAR_FILE \
    FOUNDATION_GCP_PROJECT_ID \
    FOUNDATION_GCP_REGION \
    FOUNDATION_STATE_BUCKET \
    FOUNDATION_STATE_PREFIX; do
    if [[ -z "${!variable_name+x}" ]]; then
      printf 'Missing required plan metadata context: %s\n' "${variable_name}" >&2
      exit 1
    fi
  done
  if [[ ! -f "${FOUNDATION_ENV_FILE}" ]]; then
    printf 'Foundation environment file is missing: %s\n' "${FOUNDATION_ENV_FILE}" >&2
    exit 1
  fi
}

configuration_sha256() {
  local config_root="$1"
  local repo_root="$2"
  local file
  local relative

  {
    while IFS= read -r -d '' file; do
      relative="${file#"${config_root}/"}"
      printf 'config:%s\t%s\n' "${relative}" "$(sha256_file "${file}")"
    done < <(
      find "${config_root}" \
        -path "${config_root}/.terraform" -prune -o \
        -type f \
        \( -name '*.tf' -o -name '*.tf.json' -o -name '.terraform.lock.hcl' -o -name 'Makefile' -o -path '*/scripts/*.sh' \) \
        -print0 \
        | LC_ALL=C sort -z
    )
    printf 'tool-versions\t%s\n' "$(sha256_file "${repo_root}/.tool-versions")"
    printf 'environment-file\t%s\n' "$(sha256_file "${FOUNDATION_ENV_FILE}")"
    if [[ -f "${FOUNDATION_TF_VAR_FILE}" ]]; then
      printf 'terraform-var-file\t%s\n' "$(sha256_file "${FOUNDATION_TF_VAR_FILE}")"
    else
      printf 'terraform-var-file\tabsent\n'
    fi
  } | sha256_stdin
}

identity_json() {
  local terraform_bin="$1"
  local config_root="$2"
  local repo_root="$3"
  local terraform_version
  local git_head
  local source_sha256

  require_context
  config_root="$(cd "${config_root}" && pwd -P)"
  repo_root="$(cd "${repo_root}" && pwd -P)"

  terraform_version="$("${terraform_bin}" version -json | jq -er '.terraform_version')"
  git_head="$(git -C "${repo_root}" rev-parse --verify HEAD)"
  source_sha256="$(configuration_sha256 "${config_root}" "${repo_root}")"

  jq -cn \
    --arg git_head "${git_head}" \
    --arg source_sha256 "${source_sha256}" \
    --arg terraform_version "${terraform_version}" \
    --arg environment "${FOUNDATION_ENV}" \
    --arg gcp_project_id "${FOUNDATION_GCP_PROJECT_ID}" \
    --arg gcp_region "${FOUNDATION_GCP_REGION}" \
    --arg state_bucket "${FOUNDATION_STATE_BUCKET}" \
    --arg state_prefix "${FOUNDATION_STATE_PREFIX}" \
    '{
      git_head: $git_head,
      source_sha256: $source_sha256,
      terraform_version: $terraform_version,
      environment: $environment,
      gcp_project_id: $gcp_project_id,
      gcp_region: $gcp_region,
      state_bucket: $state_bucket,
      state_prefix: $state_prefix
    }'
}

build_manifest() {
  local plan_path="$1"
  local terraform_bin="$2"
  local config_root="$3"
  local repo_root="$4"
  local expected_fingerprint="${5:-}"
  local identity
  local identity_fingerprint

  assert_private_regular_file "${plan_path}" "Saved Terraform plan"
  identity="$(identity_json "${terraform_bin}" "${config_root}" "${repo_root}")"
  identity_fingerprint="$(jq -Sc . <<<"${identity}" | sha256_stdin)"
  if [[ -n "${expected_fingerprint}" && "${identity_fingerprint}" != "${expected_fingerprint}" ]]; then
    printf 'Refusing saved plan: configuration changed before provenance could be recorded.\n' >&2
    exit 1
  fi
  jq -S \
    --arg plan_sha256 "$(sha256_file "${plan_path}")" \
    '. + {schema_version: 1, plan_sha256: $plan_sha256}' \
    <<<"${identity}"
}

case "${mode}" in
  fingerprint)
    [[ "$#" -eq 4 ]] || usage
    identity_json "$2" "$3" "$4" | jq -Sc . | sha256_stdin
    ;;
  write)
    [[ "$#" -eq 7 ]] || usage
    umask 077
    build_manifest "$2" "$4" "$5" "$6" "$7" >"$3"
    chmod 0600 "$3"
    ;;
  verify)
    [[ "$#" -eq 6 ]] || usage
    assert_private_regular_file "$3" "Saved plan manifest"
    recorded_manifest="$(jq -Sc . "$3")"
    current_manifest="$(build_manifest "$2" "$4" "$5" "$6" | jq -Sc .)"
    if [[ "${recorded_manifest}" != "${current_manifest}" ]]; then
      printf 'Refusing saved plan: provenance no longer matches the reviewed plan context.\n' >&2
      diff \
        <(jq -S . "$3") \
        <(build_manifest "$2" "$4" "$5" "$6") >&2 || true
      exit 1
    fi
    printf 'Saved foundation plan provenance verified.\n'
    ;;
  *)
    usage
    ;;
esac
