#!/usr/bin/env bash
set -euo pipefail

terraform_bin="${1:-terraform}"
umask 077
state_error="$(mktemp)"
trap 'rm -f "${state_error}"' EXIT

if state_addresses="$("${terraform_bin}" state list 2>"${state_error}")"; then
  :
elif grep -q 'No state file was found' "${state_error}"; then
  state_addresses=""
else
  printf 'Unable to inspect Terraform state:\n' >&2
  cat "${state_error}" >&2
  exit 1
fi

if [[ -z "${state_addresses}" ]]; then
  exit 0
fi

unexpected_addresses="$(
  printf '%s\n' "${state_addresses}" \
    | sed '/^[[:space:]]*$/d' \
    | grep -Ev '^module\.init\.' \
    || true
)"
forbidden_credentials="$(
  printf '%s\n' "${state_addresses}" \
    | grep -E 'google_service_account_key|google_storage_hmac_key' \
    || true
)"
leaking_acl_generators="$(
  printf '%s\n' "${state_addresses}" \
    | grep -E '(^|\.)random_uuid\.(consul_acl_token|nomad_acl_token)$' \
    || true
)"

if [[ -n "${unexpected_addresses}" \
  || -n "${forbidden_credentials}" \
  || -n "${leaking_acl_generators}" ]]; then
  printf 'Refusing foundation workflow: Terraform state is not foundation-only.\n' >&2
  if [[ -n "${unexpected_addresses}" ]]; then
    printf 'Addresses outside module.init:\n%s\n' "${unexpected_addresses}" >&2
  fi
  if [[ -n "${forbidden_credentials}" ]]; then
    printf 'Forbidden long-lived credential addresses:\n%s\n' "${forbidden_credentials}" >&2
  fi
  if [[ -n "${leaking_acl_generators}" ]]; then
    printf 'Legacy ACL token generators can disclose token values during refresh:\n%s\n' \
      "${leaking_acl_generators}" >&2
    printf "Run the guarded 'make foundation-scrub-legacy-acl-token-state' migration before any plan.\n" >&2
  fi
  exit 1
fi
