#!/usr/bin/env bash

set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
guard="${script_dir}/assert-keyless-runtime.sh"
test_dir="$(mktemp -d)"
trap 'rm -rf "$test_dir"' EXIT

"$guard"

printf '%s\n' 'runtime identity is provided by attached service account ADC' >"${test_dir}/safe.txt"
"$guard" "${test_dir}/safe.txt"

printf '%s\n' 'GOOGLE_SERVICE_ACCOUNT_KEY=forbidden' >"${test_dir}/bad.txt"
if "$guard" "${test_dir}/bad.txt" >"${test_dir}/bad.log" 2>&1; then
  echo "Expected the keyless runtime guard to reject a static credential seam." >&2
  exit 1
fi
grep -F "Static GCP credentials are forbidden" "${test_dir}/bad.log" >/dev/null

printf '%s\n' 'secret_data = random_uuid.consul_acl_token.result' >"${test_dir}/acl-bad.txt"
if "$guard" "${test_dir}/acl-bad.txt" >"${test_dir}/acl-bad.log" 2>&1; then
  echo "Expected the keyless runtime guard to reject a non-sensitive ACL token generator." >&2
  exit 1
fi
grep -F "Static GCP credentials are forbidden" "${test_dir}/acl-bad.log" >/dev/null

echo "Keyless GCP runtime guard tests passed."
