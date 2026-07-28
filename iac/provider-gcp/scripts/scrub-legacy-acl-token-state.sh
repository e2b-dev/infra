#!/usr/bin/env bash
set -euo pipefail

terraform_bin="${1:-terraform}"

legacy_addresses=(
  "module.init.random_uuid.consul_acl_token"
  "module.init.random_uuid.nomad_acl_token"
)

umask 077
state_error="$(mktemp)"
trap 'rm -f "${state_error}"' EXIT

if state_addresses="$("${terraform_bin}" state list 2>"${state_error}")"; then
  :
elif grep -q 'No state file was found' "${state_error}"; then
  printf 'No foundation state exists; no legacy ACL generator state requires scrubbing.\n'
  exit 0
else
  printf 'Unable to inspect Terraform state; no state was changed.\n' >&2
  exit 1
fi

unexpected_addresses="$(
  printf '%s\n' "${state_addresses}" \
    | sed '/^[[:space:]]*$/d' \
    | grep -Ev '^module\.init\.' \
    || true
)"
if [[ -n "${unexpected_addresses}" ]]; then
  printf 'Refusing ACL state scrub because the selected state is not foundation-only.\n' >&2
  printf 'Addresses outside module.init:\n%s\n' "${unexpected_addresses}" >&2
  exit 1
fi

unexpected_acl_generators="$(
  printf '%s\n' "${state_addresses}" \
    | grep -E '(^|\.)random_uuid\.(consul_acl_token|nomad_acl_token)$' \
    | grep -Ev '^module\.init\.random_uuid\.(consul_acl_token|nomad_acl_token)$' \
    || true
)"
if [[ -n "${unexpected_acl_generators}" ]]; then
  printf 'Refusing ACL state scrub because an unexpected legacy address was found:\n%s\n' \
    "${unexpected_acl_generators}" >&2
  exit 1
fi

addresses_to_forget=()
for address in "${legacy_addresses[@]}"; do
  if grep -Fxq "${address}" <<<"${state_addresses}"; then
    addresses_to_forget+=("${address}")
  fi
done

if [[ "${#addresses_to_forget[@]}" -eq 0 ]]; then
  printf 'Legacy ACL token generator state is already absent.\n'
  exit 0
fi

# random_uuid has no remote object. Forgetting these exact state entries is the
# only safe migration because its UUID is also its non-sensitive Terraform ID.
"${terraform_bin}" state rm -lock-timeout=5m "${addresses_to_forget[@]}"

remaining_addresses="$("${terraform_bin}" state list)"
for address in "${legacy_addresses[@]}"; do
  if grep -Fxq "${address}" <<<"${remaining_addresses}"; then
    printf 'ACL token generator state scrub did not remove %s.\n' "${address}" >&2
    exit 1
  fi
done

printf 'Forgot %s legacy ACL token generator state entries; no cloud resource was deleted.\n' \
  "${#addresses_to_forget[@]}"
