#!/usr/bin/env bash
set -euo pipefail

expected_bucket="${1:?usage: assert-foundation-backend.sh BUCKET PREFIX [TF_DATA_DIR]}"
expected_prefix="${2:?usage: assert-foundation-backend.sh BUCKET PREFIX [TF_DATA_DIR]}"
terraform_data_dir="${3:-${TF_DATA_DIR:-.terraform}}"
backend_metadata="${terraform_data_dir}/terraform.tfstate"

command -v jq >/dev/null 2>&1 || {
  printf 'jq is required to inspect Terraform backend metadata.\n' >&2
  exit 1
}

if [[ ! -f "${backend_metadata}" ]]; then
  printf 'Terraform backend metadata is missing at %s. Run foundation-init for the selected environment.\n' \
    "${backend_metadata}" >&2
  exit 1
fi

if ! backend_type="$(jq -er '.backend.type // empty' "${backend_metadata}")" \
  || ! actual_bucket="$(jq -er '.backend.config.bucket // empty' "${backend_metadata}")" \
  || ! actual_prefix="$(jq -er '.backend.config.prefix // empty' "${backend_metadata}")"; then
  printf 'Terraform backend metadata is invalid or incomplete at %s.\n' "${backend_metadata}" >&2
  exit 1
fi

if [[ "${backend_type}" != "gcs" \
  || "${actual_bucket}" != "${expected_bucket}" \
  || "${actual_prefix}" != "${expected_prefix}" ]]; then
  printf 'Refusing foundation workflow: initialized backend does not match the selected environment.\n' >&2
  printf 'Expected: type=gcs bucket=%s prefix=%s\n' "${expected_bucket}" "${expected_prefix}" >&2
  printf 'Actual:   type=%s bucket=%s prefix=%s\n' \
    "${backend_type}" "${actual_bucket}" "${actual_prefix}" >&2
  exit 1
fi
