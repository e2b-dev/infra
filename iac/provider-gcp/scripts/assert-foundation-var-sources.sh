#!/usr/bin/env bash
set -euo pipefail

config_root="${1:-.}"
explicit_var_file="${2:-}"

config_root="$(cd "${config_root}" && pwd -P)"

unexpected_sources=()
while IFS= read -r -d '' candidate; do
  unexpected_sources+=("${candidate}")
done < <(
  find "${config_root}" \
    -maxdepth 1 \
    \( \
      -name 'terraform.tfvars' \
      -o -name 'terraform.tfvars.json' \
      -o -name '*.auto.tfvars' \
      -o -name '*.auto.tfvars.json' \
    \) \
    -print0 \
    | LC_ALL=C sort -z
)

if (( ${#unexpected_sources[@]} > 0 )); then
  printf 'Refusing foundation workflow: Terraform would auto-load untracked variable sources:\n' >&2
  printf '  %s\n' "${unexpected_sources[@]}" >&2
  printf 'Move reviewed values into the explicit environment var file: %s\n' \
    "${explicit_var_file:-<not configured>}" >&2
  exit 1
fi

if [[ -n "${explicit_var_file}" \
  && ( -e "${explicit_var_file}" || -L "${explicit_var_file}" ) ]]; then
  if [[ ! -f "${explicit_var_file}" || -L "${explicit_var_file}" ]]; then
    printf 'Explicit foundation var file must be a regular, non-symlink file: %s\n' \
      "${explicit_var_file}" >&2
    exit 1
  fi
fi
