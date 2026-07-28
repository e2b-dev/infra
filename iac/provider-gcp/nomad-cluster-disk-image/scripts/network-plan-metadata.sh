#!/usr/bin/env bash
set -euo pipefail

mode="${1:-}"

usage() {
  cat >&2 <<'EOF'
usage:
  network-plan-metadata.sh fingerprint TERRAFORM_BIN PACKER_BIN GCLOUD_BIN CONFIG_ROOT REPO_ROOT
  network-plan-metadata.sh write PLAN MANIFEST TERRAFORM_BIN PACKER_BIN GCLOUD_BIN CONFIG_ROOT REPO_ROOT EXPECTED_FINGERPRINT
  network-plan-metadata.sh verify PLAN MANIFEST TERRAFORM_BIN PACKER_BIN GCLOUD_BIN CONFIG_ROOT REPO_ROOT
  network-plan-metadata.sh verify-build-inputs MANIFEST TERRAFORM_BIN PACKER_BIN GCLOUD_BIN CONFIG_ROOT REPO_ROOT
  network-plan-metadata.sh confirmation MANIFEST

Required environment:
  PACKER_GATE_ENV
  PACKER_GATE_ENV_FILE
  PACKER_GATE_GCP_PROJECT_ID
  PACKER_GATE_GCP_REGION
  PACKER_GATE_GCP_ZONE
  PACKER_GATE_STATE_BUCKET
  PACKER_GATE_STATE_PREFIX
  PACKER_GATE_NETWORK_NAME
  PACKER_GATE_SUBNET_NAME
  PACKER_GATE_CONSUL_VERSION
  PACKER_GATE_NOMAD_VERSION
  PACKER_GATE_SOURCE_IMAGE
  PACKER_GATE_IMAGE_NAME
  PACKER_GATE_IMAGE_FAMILY
  PACKER_GATE_CANONICAL_IMAGE_FAMILY
  PACKER_GATE_SOURCE_REVISION
  PACKER_PLUGIN_PATH

Optional environment:
  PACKER_GATE_MAX_PLAN_AGE_SECONDS (default 3600)
EOF
  exit 2
}

sha256_file() {
  shasum -a 256 "$1" | awk '{print $1}'
}

sha256_stdin() {
  shasum -a 256 | awk '{print $1}'
}

file_mode() {
  if stat -c '%a' "$1" >/dev/null 2>&1; then
    stat -c '%a' "$1"
  else
    stat -f '%Lp' "$1"
  fi
}

assert_private_regular_file() {
  local path="$1"
  local label="$2"
  local mode_bits

  if [[ ! -f "${path}" || -L "${path}" ]]; then
    printf '%s must be a regular, non-symlink file: %s\n' "${label}" "${path}" >&2
    exit 1
  fi

  mode_bits="$(file_mode "${path}")"
  if (( (8#${mode_bits} & 077) != 0 )); then
    printf '%s must not be readable or writable by group/other: %s (mode %s)\n' \
      "${label}" "${path}" "${mode_bits}" >&2
    exit 1
  fi
}

require_context() {
  local variable_name
  for variable_name in \
    PACKER_GATE_ENV \
    PACKER_GATE_ENV_FILE \
    PACKER_GATE_GCP_PROJECT_ID \
    PACKER_GATE_GCP_REGION \
    PACKER_GATE_GCP_ZONE \
    PACKER_GATE_STATE_BUCKET \
    PACKER_GATE_STATE_PREFIX \
    PACKER_GATE_NETWORK_NAME \
    PACKER_GATE_SUBNET_NAME \
    PACKER_GATE_CONSUL_VERSION \
    PACKER_GATE_NOMAD_VERSION \
    PACKER_GATE_SOURCE_IMAGE \
    PACKER_GATE_IMAGE_NAME \
    PACKER_GATE_IMAGE_FAMILY \
    PACKER_GATE_CANONICAL_IMAGE_FAMILY \
    PACKER_GATE_SOURCE_REVISION \
    PACKER_PLUGIN_PATH; do
    if [[ -z "${!variable_name+x}" ]]; then
      printf 'Missing required network-plan metadata context: %s\n' \
        "${variable_name}" >&2
      exit 1
    fi
  done

  if [[ ! -f "${PACKER_GATE_ENV_FILE}" || -L "${PACKER_GATE_ENV_FILE}" ]]; then
    printf 'Operator environment file must be a regular, non-symlink file: %s\n' \
      "${PACKER_GATE_ENV_FILE}" >&2
    exit 1
  fi
}

configuration_sha256() {
  local config_root="$1"
  local repo_root="$2"
  local file
  local relative
  local -a fixed_inputs=(
    "${config_root}/.terraform.lock.hcl"
    "${config_root}/Makefile"
    "${config_root}/main.pkr.hcl"
    "${config_root}/main.tf"
    "${config_root}/setup/gc-ops.config.yaml"
    "${config_root}/setup/root-artifacts.lock.json"
    "${config_root}/variables.pkr.hcl"
    "${config_root}/variables.tf"
    "${repo_root}/.tool-versions"
    "${repo_root}/iac/provider-gcp/topology/minimal-workload-policy.json"
  )

  {
    {
      printf '%s\0' "${fixed_inputs[@]}"
      find "${config_root}/scripts" -type f -print0
      find "${repo_root}/iac/nomad-cluster-disk-image/setup" -type f -print0
    } | LC_ALL=C sort -zu |
      while IFS= read -r -d '' file; do
        if [[ ! -f "${file}" || -L "${file}" ]]; then
          printf 'Reviewed source input is missing or unsafe: %s\n' "${file}" >&2
          exit 1
        fi
        relative="${file#"${repo_root}/"}"
        printf 'source:%s\tmode:%s\tsha256:%s\n' \
          "${relative}" "$(file_mode "${file}")" "$(sha256_file "${file}")"
      done
  } | sha256_stdin
}

packer_version() {
  "$1" version -machine-readable |
    awk -F, '$3 == "version" { print $4; exit }'
}

state_identity_json() {
  local terraform_bin="$1"
  local state_json

  if ! state_json="$("${terraform_bin}" state pull 2>/dev/null)" \
    || [[ -z "${state_json}" ]]; then
    jq -cn '{exists: false, lineage: null, serial: null}'
    return
  fi

  jq -ce '
    select(
      (.lineage | type) == "string"
      and (.serial | type) == "number"
      and .serial >= 0
    )
    | {
        exists: true,
        lineage,
        serial
      }
  ' <<<"${state_json}" || {
    printf 'Unable to extract a safe Terraform state lineage/serial identity.\n' >&2
    exit 1
  }
}

identity_json() {
  local terraform_bin="$1"
  local packer_bin="$2"
  local gcloud_bin="$3"
  local config_root="$4"
  local repo_root="$5"
  local terraform_version
  local packer_version_value
  local gcloud_version
  local git_head
  local source_sha256
  local plugin_identity
  local source_image_identity
  local state_identity

  require_context
  config_root="$(cd "${config_root}" && pwd -P)"
  repo_root="$(cd "${repo_root}" && pwd -P)"

  terraform_version="$("${terraform_bin}" version -json | jq -er '.terraform_version')"
  packer_version_value="$(packer_version "${packer_bin}")"
  [[ -n "${packer_version_value}" ]] || {
    printf 'Unable to determine Packer version.\n' >&2
    exit 1
  }
  gcloud_version="$(
    "${gcloud_bin}" version --format=json |
      jq -er '."Google Cloud SDK"'
  )"
  plugin_identity="$(
    "$(dirname "${BASH_SOURCE[0]}")/assert-packer-plugin.sh" \
      "${packer_bin}" "${PACKER_PLUGIN_PATH}"
  )"
  source_image_identity="$(
    "$(dirname "${BASH_SOURCE[0]}")/resolve-source-image.sh" \
      "${gcloud_bin}" "ubuntu-os-cloud" "${PACKER_GATE_SOURCE_IMAGE}"
  )"
  state_identity="$(state_identity_json "${terraform_bin}")"
  git_head="$(git -C "${repo_root}" rev-parse --verify HEAD)"
  source_sha256="$(configuration_sha256 "${config_root}" "${repo_root}")"

  [[ "${PACKER_GATE_SOURCE_REVISION}" == "${git_head}" ]] || {
    printf 'Packer source revision must equal the exact checked-out Git HEAD.\n' >&2
    exit 1
  }

  jq -cn \
    --arg git_head "${git_head}" \
    --arg source_sha256 "${source_sha256}" \
    --arg terraform_version "${terraform_version}" \
    --arg packer_version "${packer_version_value}" \
    --arg gcloud_version "${gcloud_version}" \
    --arg terraform_path "$(cd "$(dirname "${terraform_bin}")" && pwd -P)/$(basename "${terraform_bin}")" \
    --arg terraform_sha256 "$(sha256_file "${terraform_bin}")" \
    --arg packer_path "$(cd "$(dirname "${packer_bin}")" && pwd -P)/$(basename "${packer_bin}")" \
    --arg packer_sha256 "$(sha256_file "${packer_bin}")" \
    --arg gcloud_path "$(cd "$(dirname "${gcloud_bin}")" && pwd -P)/$(basename "${gcloud_bin}")" \
    --arg gcloud_sha256 "$(sha256_file "${gcloud_bin}")" \
    --argjson packer_plugin "${plugin_identity}" \
    --argjson source_image_identity "${source_image_identity}" \
    --argjson state_identity "${state_identity}" \
    --arg environment_file "$(cd "$(dirname "${PACKER_GATE_ENV_FILE}")" && pwd -P)/$(basename "${PACKER_GATE_ENV_FILE}")" \
    --arg environment "${PACKER_GATE_ENV}" \
    --arg gcp_project_id "${PACKER_GATE_GCP_PROJECT_ID}" \
    --arg gcp_region "${PACKER_GATE_GCP_REGION}" \
    --arg gcp_zone "${PACKER_GATE_GCP_ZONE}" \
    --arg state_bucket "${PACKER_GATE_STATE_BUCKET}" \
    --arg state_prefix "${PACKER_GATE_STATE_PREFIX}" \
    --arg network_name "${PACKER_GATE_NETWORK_NAME}" \
    --arg subnet_name "${PACKER_GATE_SUBNET_NAME}" \
    --arg consul_version "${PACKER_GATE_CONSUL_VERSION}" \
    --arg nomad_version "${PACKER_GATE_NOMAD_VERSION}" \
    --arg source_image "${PACKER_GATE_SOURCE_IMAGE}" \
    --arg image_name "${PACKER_GATE_IMAGE_NAME}" \
    --arg image_family "${PACKER_GATE_IMAGE_FAMILY}" \
    --arg canonical_image_family "${PACKER_GATE_CANONICAL_IMAGE_FAMILY}" \
    --arg source_revision "${PACKER_GATE_SOURCE_REVISION}" \
    '{
      git_head: $git_head,
      source_sha256: $source_sha256,
      terraform_version: $terraform_version,
      packer_version: $packer_version,
      gcloud_version: $gcloud_version,
      terraform_path: $terraform_path,
      terraform_sha256: $terraform_sha256,
      packer_path: $packer_path,
      packer_sha256: $packer_sha256,
      gcloud_path: $gcloud_path,
      gcloud_sha256: $gcloud_sha256,
      packer_plugin: $packer_plugin,
      source_image_identity: $source_image_identity,
      state_identity: $state_identity,
      environment_file: $environment_file,
      environment: $environment,
      gcp_project_id: $gcp_project_id,
      gcp_region: $gcp_region,
      gcp_zone: $gcp_zone,
      state_bucket: $state_bucket,
      state_prefix: $state_prefix,
      network_name: $network_name,
      subnet_name: $subnet_name,
      consul_version: $consul_version,
      nomad_version: $nomad_version,
      source_image: $source_image,
      source_image_project: "ubuntu-os-cloud",
      image_name: $image_name,
      image_family: $image_family,
      canonical_image_family: $canonical_image_family,
      source_revision: $source_revision
    }'
}

build_manifest() {
  local plan_path="$1"
  local terraform_bin="$2"
  local packer_bin="$3"
  local gcloud_bin="$4"
  local config_root="$5"
  local repo_root="$6"
  local expected_fingerprint="${7:-}"
  local identity
  local identity_fingerprint

  assert_private_regular_file "${plan_path}" "Saved Terraform plan"
  identity="$(
    identity_json \
      "${terraform_bin}" "${packer_bin}" "${gcloud_bin}" \
      "${config_root}" "${repo_root}"
  )"
  identity_fingerprint="$(jq -Sc . <<<"${identity}" | sha256_stdin)"

  if [[ -n "${expected_fingerprint}" \
    && "${identity_fingerprint}" != "${expected_fingerprint}" ]]; then
    printf 'Refusing saved network plan: inputs changed before provenance was recorded.\n' >&2
    exit 1
  fi

  jq -S \
    --arg plan_sha256 "$(sha256_file "${plan_path}")" \
    --argjson created_at_epoch "$(date -u +%s)" \
    '. + {
      schema_version: 2,
      plan_sha256: $plan_sha256,
      created_at_epoch: $created_at_epoch
    }' <<<"${identity}"
}

assert_fresh_manifest() {
  local manifest_path="$1"
  local max_age="${PACKER_GATE_MAX_PLAN_AGE_SECONDS:-3600}"
  local created_at
  local now
  local age

  [[ "${max_age}" =~ ^[1-9][0-9]{0,5}$ ]] || {
    printf 'Invalid PACKER_GATE_MAX_PLAN_AGE_SECONDS: %s\n' "${max_age}" >&2
    exit 1
  }
  created_at="$(jq -er 'select(.schema_version == 2) | .created_at_epoch' "${manifest_path}")"
  now="$(date -u +%s)"
  age="$((now - created_at))"
  if (( age < 0 || age > max_age )); then
    printf 'Saved plan is outside the %s-second review window (age %s seconds).\n' \
      "${max_age}" "${age}" >&2
    exit 1
  fi
}

case "${mode}" in
  fingerprint)
    [[ "$#" -eq 6 ]] || usage
    identity_json "$2" "$3" "$4" "$5" "$6" | jq -Sc . | sha256_stdin
    ;;
  write)
    [[ "$#" -eq 9 ]] || usage
    umask 077
    build_manifest "$2" "$4" "$5" "$6" "$7" "$8" "$9" >"$3"
    chmod 0600 "$3"
    ;;
  verify)
    [[ "$#" -eq 8 ]] || usage
    assert_private_regular_file "$3" "Saved plan manifest"
    assert_fresh_manifest "$3"
    recorded_manifest="$(jq -Sc . "$3")"
    current_identity="$(
      identity_json "$4" "$5" "$6" "$7" "$8"
    )"
    current_manifest="$(
      jq -Sc \
        --arg plan_sha256 "$(sha256_file "$2")" \
        --argjson created_at_epoch "$(jq -er '.created_at_epoch' "$3")" \
        '. + {
          schema_version: 2,
          plan_sha256: $plan_sha256,
          created_at_epoch: $created_at_epoch
        }' <<<"${current_identity}"
    )"
    if [[ "${recorded_manifest}" != "${current_manifest}" ]]; then
      printf 'Refusing saved network plan: provenance no longer matches the reviewed context.\n' >&2
      diff <(jq -S . "$3") <(jq -S . <<<"${current_manifest}") >&2 || true
      exit 1
    fi
    printf 'Saved network-plan provenance, source image, and state serial verified.\n'
    ;;
  verify-build-inputs)
    [[ "$#" -eq 7 ]] || usage
    assert_private_regular_file "$2" "Saved plan manifest"
    assert_fresh_manifest "$2"
    recorded_identity="$(jq -Sc 'del(.schema_version, .plan_sha256, .created_at_epoch, .state_identity)' "$2")"
    current_identity="$(
      identity_json "$3" "$4" "$5" "$6" "$7" |
        jq -Sc 'del(.state_identity)'
    )"
    if [[ "${recorded_identity}" != "${current_identity}" ]]; then
      printf 'Refusing Packer build: reviewed build inputs changed after network apply.\n' >&2
      exit 1
    fi
    printf 'Post-apply Packer inputs and immutable source image still match provenance.\n'
    ;;
  confirmation)
    [[ "$#" -eq 2 ]] || usage
    assert_private_regular_file "$2" "Saved plan manifest"
    manifest_sha256="$(sha256_file "$2")"
    jq -er \
      --arg manifest_sha256 "${manifest_sha256}" \
      '"APPLY PACKER CANARY manifest=" + $manifest_sha256
        + " env=" + .environment
        + " project=" + .gcp_project_id
        + " image=" + .image_name' "$2"
    ;;
  *)
    usage
    ;;
esac
