#!/usr/bin/env bash
set -euo pipefail

terraform_bin="${1:-terraform}"

umask 077
state_error="$(mktemp)"
trap 'rm -f "${state_error}"' EXIT

if state_addresses="$("${terraform_bin}" state list 2>"${state_error}")"; then
  :
elif grep -q 'No state file was found' "${state_error}"; then
  exit 0
else
  printf 'Unable to prove legacy ACL token generators are absent from Terraform state.\n' >&2
  exit 1
fi

leaking_acl_generators="$(
  printf '%s\n' "${state_addresses}" \
    | grep -E '(^|\.)random_uuid\.(consul_acl_token|nomad_acl_token)$' \
    || true
)"

if [[ -n "${leaking_acl_generators}" ]]; then
  printf 'Refusing Terraform refresh/plan: legacy ACL generators can disclose token values.\n' >&2
  printf '%s\n' "${leaking_acl_generators}" >&2
  printf "Run the guarded 'make foundation-scrub-legacy-acl-token-state' migration first.\n" >&2
  exit 1
fi
