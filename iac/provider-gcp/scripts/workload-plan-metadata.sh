#!/usr/bin/env bash
set -euo pipefail

mode="${1:-}"

usage() {
  cat >&2 <<'EOF'
usage:
  workload-plan-metadata.sh fingerprint TERRAFORM_BIN CONFIG_ROOT REPO_ROOT ARTIFACTS
  workload-plan-metadata.sh write PLAN MANIFEST TERRAFORM_BIN CONFIG_ROOT REPO_ROOT ARTIFACTS EXPECTED_FINGERPRINT
  workload-plan-metadata.sh verify PLAN MANIFEST TERRAFORM_BIN CONFIG_ROOT REPO_ROOT ARTIFACTS

Required environment:
  WORKLOAD_ENV
  WORKLOAD_ENV_FILE
  WORKLOAD_TF_VAR_FILE
  WORKLOAD_GCP_PROJECT_ID
  WORKLOAD_GCP_REGION
  WORKLOAD_GCP_ZONE
  WORKLOAD_PREFIX
  WORKLOAD_CORE_IMAGE_REVISION
  WORKLOAD_JOB_BINARY_BUCKET
  WORKLOAD_STATE_BUCKET
  WORKLOAD_STATE_PREFIX
  WORKLOAD_TOPOLOGY_POLICY
  WORKLOAD_PACKER_TEMPLATE
EOF
  exit 2
}

sha256_file() {
  shasum -a 256 "$1" | awk '{print $1}'
}

sha256_stdin() {
  shasum -a 256 | awk '{print $1}'
}

assert_regular_file() {
  local path="$1"
  local label="$2"

  if [[ ! -f "${path}" || -L "${path}" ]]; then
    printf '%s must be a regular, non-symlink file: %s\n' "${label}" "${path}" >&2
    exit 1
  fi
}

assert_private_regular_file() {
  local path="$1"
  local label="$2"
  local mode_bits

  assert_regular_file "${path}" "${label}"
  if mode_bits="$(stat -c '%a' "${path}" 2>/dev/null)"; then
    :
  else
    mode_bits="$(stat -f '%Lp' "${path}")"
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
    WORKLOAD_ENV \
    WORKLOAD_ENV_FILE \
    WORKLOAD_TF_VAR_FILE \
    WORKLOAD_GCP_PROJECT_ID \
    WORKLOAD_GCP_REGION \
    WORKLOAD_GCP_ZONE \
    WORKLOAD_PREFIX \
    WORKLOAD_CORE_IMAGE_REVISION \
    WORKLOAD_JOB_BINARY_BUCKET \
    WORKLOAD_STATE_BUCKET \
    WORKLOAD_STATE_PREFIX \
    WORKLOAD_TOPOLOGY_POLICY \
    WORKLOAD_PACKER_TEMPLATE; do
    if [[ -z "${!variable_name+x}" || -z "${!variable_name}" ]]; then
      printf 'Missing required workload plan metadata context: %s\n' "${variable_name}" >&2
      exit 1
    fi
  done

  assert_regular_file "${WORKLOAD_ENV_FILE}" "Workload environment file"
  assert_regular_file "${WORKLOAD_TF_VAR_FILE}" "Explicit workload Terraform var file"
  assert_regular_file "${WORKLOAD_TOPOLOGY_POLICY}" "Workload topology policy"
  assert_regular_file "${WORKLOAD_PACKER_TEMPLATE}" "Workload Packer template"
}

configuration_sha256() {
  local config_root="$1"
  local repo_root="$2"
  local modules_root
  local file
  local relative

  modules_root="${repo_root}/iac/modules"
  [[ -d "${modules_root}" ]] || {
    printf 'Local Terraform modules directory is missing: %s\n' \
      "${modules_root}" >&2
    exit 1
  }
  if find "${config_root}" "${modules_root}" -type l -print -quit | grep -q .; then
    printf 'Workload Terraform release inputs must not contain symlinks.\n' >&2
    exit 1
  fi

  {
    while IFS= read -r -d '' file; do
      relative="${file#"${config_root}/"}"
      printf 'config:%s\t%s\n' "${relative}" "$(sha256_file "${file}")"
    done < <(
      find "${config_root}" \
        -path "${config_root}/.terraform" -prune -o \
        -path "${config_root}/.packer-plugins" -prune -o \
        -path "${config_root}/.workload-plan.*" -prune -o \
        -path "${config_root}/.workload-apply.*" -prune -o \
        -path "${config_root}/.workload-plan-check.*" -prune -o \
        -path "${config_root}/.workload-cluster-plan.*" -prune -o \
        -path "${config_root}/.workload-cluster-apply.*" -prune -o \
        -path "${config_root}/.workload-cluster-plan-check.*" -prune -o \
        -path "${config_root}/.workload-prerequisite-plan.*" -prune -o \
        -path "${config_root}/.workload-prerequisite-apply.*" -prune -o \
        -path "${config_root}/.workload-prerequisite-plan-check.*" -prune -o \
        -type f \
        ! -name '.tfplan*' \
        ! -name '*.tfstate' \
        ! -name '*.tfstate.*' \
        ! -name '.terraform.tfstate.lock.info' \
        -print0 \
        | LC_ALL=C sort -z
    )
    while IFS= read -r -d '' file; do
      relative="${file#"${modules_root}/"}"
      printf 'module:%s\t%s\n' "${relative}" "$(sha256_file "${file}")"
    done < <(
      find "${modules_root}" \
        -path "${modules_root}/.terraform" -prune -o \
        -path "${modules_root}/.packer-plugins" -prune -o \
        -type f \
        ! -name '.tfplan*' \
        ! -name '*.tfstate' \
        ! -name '*.tfstate.*' \
        ! -name '.terraform.tfstate.lock.info' \
        -print0 \
        | LC_ALL=C sort -z
    )
    printf 'tool-versions\t%s\n' "$(sha256_file "${repo_root}/.tool-versions")"
  } | sha256_stdin
}

packer_inputs_sha256() {
  local config_root="$1"
  local repo_root="$2"
  local packer_root
  local shared_setup_root
  local file
  local relative

  packer_root="$(cd "$(dirname "${WORKLOAD_PACKER_TEMPLATE}")" && pwd -P)"
  shared_setup_root="${repo_root}/iac/nomad-cluster-disk-image/setup"
  [[ -d "${shared_setup_root}" ]] || {
    printf 'Shared Packer setup directory is missing: %s\n' \
      "${shared_setup_root}" >&2
    exit 1
  }
  if find "${packer_root}" \
      -path "${packer_root}/.terraform" -prune -o \
      -path "${packer_root}/.packer-plugins" -prune -o \
      -type l -print -quit \
    | grep -q . \
    || find "${shared_setup_root}" -type l -print -quit \
    | grep -q .; then
    printf 'Packer release inputs must not contain symlinks.\n' >&2
    exit 1
  fi

  {
    while IFS= read -r -d '' file; do
      relative="${file#"${config_root}/"}"
      printf 'provider:%s\t%s\n' "${relative}" "$(sha256_file "${file}")"
    done < <(
      find "${packer_root}" \
        -path "${packer_root}/.terraform" -prune -o \
        -path "${packer_root}/.packer-plugins" -prune -o \
        -type f -print0 \
        | LC_ALL=C sort -z
    )
    while IFS= read -r -d '' file; do
      relative="${file#"${repo_root}/"}"
      printf 'shared:%s\t%s\n' "${relative}" "$(sha256_file "${file}")"
    done < <(
      find "${shared_setup_root}" -type f -print0 | LC_ALL=C sort -z
    )
  } | sha256_stdin
}

identity_json() {
  local terraform_bin="$1"
  local config_root="$2"
  local repo_root="$3"
  local artifacts_path="$4"
  local terraform_version
  local git_head
  local source_sha256
  local lock_file
  local release_artifacts

  require_context
  config_root="$(cd "${config_root}" && pwd -P)"
  repo_root="$(cd "${repo_root}" && pwd -P)"
  lock_file="${config_root}/.terraform.lock.hcl"
  assert_regular_file "${lock_file}" "Terraform dependency lock file"
  assert_private_regular_file "${artifacts_path}" "Resolved workload artifacts"

  terraform_version="$("${terraform_bin}" version -json | jq -er '.terraform_version')"
  git_head="$(git -C "${repo_root}" rev-parse --verify HEAD)"
  source_sha256="$(configuration_sha256 "${config_root}" "${repo_root}")"
  release_artifacts="$(
    jq -ceS \
      --arg project_id "${WORKLOAD_GCP_PROJECT_ID}" \
      --arg region "${WORKLOAD_GCP_REGION}" \
      --arg repository "${WORKLOAD_PREFIX}core" \
      --arg core_image_revision "${WORKLOAD_CORE_IMAGE_REVISION}" \
      --arg job_binary_bucket "${WORKLOAD_JOB_BINARY_BUCKET}" '
        if (
          .schema_version == 2
          and .gcp_project_id == $project_id
          and .gcp_region == $region
          and .core_repository == $repository
          and .core_image_revision == $core_image_revision
          and .job_binary_bucket == $job_binary_bucket
          and (.orchestrator_image.self_link | type) == "string"
          and (.core_images | type) == "object"
          and (.core_images | length) == 5
          and (.job_binaries | type) == "object"
          and (.job_binaries | keys | sort) == [
            "clean-nfs-cache",
            "orchestrator",
            "template-manager"
          ]
        )
        then .
        else error("resolved artifact identity does not match workload context")
        end
      ' "${artifacts_path}"
  )"

  jq -cn \
    --arg git_head "${git_head}" \
    --arg source_sha256 "${source_sha256}" \
    --arg terraform_lock_sha256 "$(sha256_file "${lock_file}")" \
    --arg environment_file_sha256 "$(sha256_file "${WORKLOAD_ENV_FILE}")" \
    --arg terraform_var_file_sha256 "$(sha256_file "${WORKLOAD_TF_VAR_FILE}")" \
    --arg topology_policy_sha256 "$(sha256_file "${WORKLOAD_TOPOLOGY_POLICY}")" \
    --arg packer_template_sha256 "$(sha256_file "${WORKLOAD_PACKER_TEMPLATE}")" \
    --arg packer_inputs_sha256 "$(packer_inputs_sha256 "${config_root}" "${repo_root}")" \
    --arg release_artifacts_sha256 "$(
      jq -Sc . <<<"${release_artifacts}" | sha256_stdin
    )" \
    --argjson release_artifacts "${release_artifacts}" \
    --arg terraform_version "${terraform_version}" \
    --arg environment "${WORKLOAD_ENV}" \
    --arg gcp_project_id "${WORKLOAD_GCP_PROJECT_ID}" \
    --arg gcp_region "${WORKLOAD_GCP_REGION}" \
    --arg gcp_zone "${WORKLOAD_GCP_ZONE}" \
    --arg prefix "${WORKLOAD_PREFIX}" \
    --arg core_image_revision "${WORKLOAD_CORE_IMAGE_REVISION}" \
    --arg job_binary_bucket "${WORKLOAD_JOB_BINARY_BUCKET}" \
    --arg state_bucket "${WORKLOAD_STATE_BUCKET}" \
    --arg state_prefix "${WORKLOAD_STATE_PREFIX}" \
    '{
      git_head: $git_head,
      source_sha256: $source_sha256,
      terraform_lock_sha256: $terraform_lock_sha256,
      environment_file_sha256: $environment_file_sha256,
      terraform_var_file_sha256: $terraform_var_file_sha256,
      topology_policy_sha256: $topology_policy_sha256,
      packer_template_sha256: $packer_template_sha256,
      packer_inputs_sha256: $packer_inputs_sha256,
      release_artifacts_sha256: $release_artifacts_sha256,
      release_artifacts: $release_artifacts,
      terraform_version: $terraform_version,
      environment: $environment,
      gcp_project_id: $gcp_project_id,
      gcp_region: $gcp_region,
      gcp_zone: $gcp_zone,
      prefix: $prefix,
      core_image_revision: $core_image_revision,
      job_binary_bucket: $job_binary_bucket,
      backend: {
        type: "gcs",
        bucket: $state_bucket,
        prefix: $state_prefix,
        workspace: "default"
      }
    }'
}

build_manifest() {
  local plan_path="$1"
  local terraform_bin="$2"
  local config_root="$3"
  local repo_root="$4"
  local artifacts_path="$5"
  local expected_fingerprint="${6:-}"
  local identity
  local identity_fingerprint

  assert_private_regular_file "${plan_path}" "Saved workload plan"
  identity="$(
    identity_json \
      "${terraform_bin}" "${config_root}" "${repo_root}" "${artifacts_path}"
  )"
  identity_fingerprint="$(jq -Sc . <<<"${identity}" | sha256_stdin)"
  if [[ -n "${expected_fingerprint}" && "${identity_fingerprint}" != "${expected_fingerprint}" ]]; then
    printf 'Refusing saved workload plan: configuration changed before provenance could be recorded.\n' >&2
    exit 1
  fi

  jq -S \
    --arg plan_sha256 "$(sha256_file "${plan_path}")" \
    '. + {schema_version: 1, plan_sha256: $plan_sha256}' \
    <<<"${identity}"
}

case "${mode}" in
  fingerprint)
    [[ "$#" -eq 5 ]] || usage
    identity_json "$2" "$3" "$4" "$5" | jq -Sc . | sha256_stdin
    ;;
  write)
    [[ "$#" -eq 8 ]] || usage
    umask 077
    build_manifest "$2" "$4" "$5" "$6" "$7" "$8" >"$3"
    chmod 0600 "$3"
    ;;
  verify)
    [[ "$#" -eq 7 ]] || usage
    assert_private_regular_file "$3" "Saved workload plan manifest"
    recorded_manifest="$(jq -Sc . "$3")"
    current_manifest="$(build_manifest "$2" "$4" "$5" "$6" "$7" | jq -Sc .)"
    if [[ "${recorded_manifest}" != "${current_manifest}" ]]; then
      printf 'Refusing saved workload plan: provenance no longer matches the reviewed context.\n' >&2
      diff \
        <(jq -S . "$3") \
        <(build_manifest "$2" "$4" "$5" "$6" "$7") >&2 || true
      exit 1
    fi
    printf 'Saved workload plan provenance verified.\n'
    ;;
  *)
    usage
    ;;
esac
