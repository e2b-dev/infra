#!/usr/bin/env bash
set -euo pipefail

terraform_bin="${1:-terraform}"

if [[ -n "${TF_WORKSPACE:-}" && "${TF_WORKSPACE}" != "default" ]]; then
  printf 'Refusing foundation workflow with TF_WORKSPACE=%s; per-environment backend prefixes require the default workspace.\n' \
    "${TF_WORKSPACE}" >&2
  exit 1
fi

actual_workspace="$("${terraform_bin}" workspace show)"
if [[ "${actual_workspace}" != "default" ]]; then
  printf 'Refusing foundation workflow in Terraform workspace %s; select the default workspace first.\n' \
    "${actual_workspace}" >&2
  exit 1
fi
