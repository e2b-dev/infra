#!/usr/bin/env bash
set -euo pipefail

config_root="${1:?usage: assert-operator-inputs.sh CONFIG_ROOT REPO_ROOT EXPECTED_PLUGIN_ROOT [REQUIRE_CLEAN]}"
repo_root="${2:?usage: assert-operator-inputs.sh CONFIG_ROOT REPO_ROOT EXPECTED_PLUGIN_ROOT [REQUIRE_CLEAN]}"
expected_plugin_root="${3:?usage: assert-operator-inputs.sh CONFIG_ROOT REPO_ROOT EXPECTED_PLUGIN_ROOT [REQUIRE_CLEAN]}"
require_clean="${4:-true}"

config_root="$(cd "${config_root}" && pwd -P)"
repo_root="$(cd "${repo_root}" && pwd -P)"
shared_setup_root="${repo_root}/iac/nomad-cluster-disk-image/setup"

if [[ "${PACKER_PLUGIN_PATH:-}" != "${expected_plugin_root}" ]]; then
  printf 'PACKER_PLUGIN_PATH must be the isolated reviewed directory: %s\n' \
    "${expected_plugin_root}" >&2
  exit 1
fi

for variable_name in \
  AUTO_CONFIRM_DEPLOY \
  CLOUDSDK_AUTH_ACCESS_TOKEN \
  CLOUDSDK_AUTH_CREDENTIAL_FILE_OVERRIDE \
  CLOUDSDK_CONFIG \
  GOOGLE_APPLICATION_CREDENTIALS \
  GOOGLE_CREDENTIALS \
  GOOGLE_OAUTH_ACCESS_TOKEN \
  HCP_PACKER_BUILD_FINGERPRINT \
  HCP_PACKER_REGISTRY \
  PACKER_CONFIG \
  PACKER_CONFIG_DIR \
  PACKER_ENFORCED_PROVISIONERS \
  TF_CLI_CONFIG_FILE \
  TF_DATA_DIR \
  TF_WORKSPACE; do
  if [[ -n "${!variable_name+x}" ]]; then
    printf 'Refusing ambient operator input: %s\n' "${variable_name}" >&2
    exit 1
  fi
done

while IFS='=' read -r variable_name _; do
  case "${variable_name}" in
    TF_CLI_ARGS*)
      printf 'Refusing ambient Terraform CLI arguments: %s\n' \
        "${variable_name}" >&2
      exit 1
      ;;
    PKR_VAR_*)
      printf 'Refusing ambient Packer variable: %s\n' "${variable_name}" >&2
      exit 1
      ;;
    HCP_PACKER_*)
      printf 'Refusing ambient HCP Packer configuration: %s\n' \
        "${variable_name}" >&2
      exit 1
      ;;
  esac
done < <(env)

for root in "${config_root}" "${shared_setup_root}"; do
  unsafe_symlink="$(find "${root}" -type l -print -quit)"
  if [[ -n "${unsafe_symlink}" ]]; then
    printf 'Operator/Packer inputs must not contain symlinks: %s\n' \
      "${unsafe_symlink}" >&2
    exit 1
  fi
done

actual_terraform_files="$(
  find "${config_root}" -maxdepth 1 -type f -name '*.tf' -print \
    | LC_ALL=C sort
)"
expected_terraform_files="$(
  printf '%s\n' \
    "${config_root}/main.tf" \
    "${config_root}/variables.tf" \
    | LC_ALL=C sort
)"
if [[ "${actual_terraform_files}" != "${expected_terraform_files}" ]]; then
  printf 'Unexpected Terraform source in operator-canary root.\n' >&2
  printf 'Observed:\n%s\n' "${actual_terraform_files:-<none>}" >&2
  exit 1
fi

actual_packer_files="$(
  find "${config_root}" -maxdepth 1 -type f -name '*.pkr.hcl' -print \
    | LC_ALL=C sort
)"
expected_packer_files="$(
  printf '%s\n' \
    "${config_root}/main.pkr.hcl" \
    "${config_root}/variables.pkr.hcl" \
    | LC_ALL=C sort
)"
if [[ "${actual_packer_files}" != "${expected_packer_files}" ]]; then
  printf 'Unexpected Packer source in operator-canary root.\n' >&2
  printf 'Observed:\n%s\n' "${actual_packer_files:-<none>}" >&2
  exit 1
fi

implicit_var_file="$(
  find "${config_root}" -maxdepth 1 -type f \
    \( \
      -name 'terraform.tfvars' -o \
      -name 'terraform.tfvars.json' -o \
      -name '*.auto.tfvars' -o \
      -name '*.auto.tfvars.json' -o \
      -name '*.auto.pkrvars.hcl' \
    \) \
    -print -quit
)"
if [[ -n "${implicit_var_file}" ]]; then
  printf 'Implicit Terraform/Packer variable files are forbidden: %s\n' \
    "${implicit_var_file}" >&2
  exit 1
fi

if [[ "${require_clean}" != "true" && "${require_clean}" != "false" ]]; then
  printf 'REQUIRE_CLEAN must be true or false.\n' >&2
  exit 1
fi

if [[ "${require_clean}" == "true" \
  && -n "$(git -C "${repo_root}" status --porcelain --untracked-files=all)" ]]; then
  printf 'Operator-canary plan/build requires a clean Git worktree.\n' >&2
  git -C "${repo_root}" status --short >&2
  exit 1
fi

printf 'Operator inputs are explicit, isolated, non-symlinked, and committed.\n'
