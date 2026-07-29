#!/usr/bin/env bash

set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
guard="${script_dir}/assert-keyless-runtime.sh"
cloud_build_tf="${script_dir}/../init/cloud-build.tf"
test_dir="$(mktemp -d)"
trap 'rm -rf "$test_dir"' EXIT

"$guard"

printf '%s\n' 'runtime identity is provided by attached service account ADC' >"${test_dir}/safe.txt"
"$guard" "${test_dir}/safe.txt"

mkdir -p "${test_dir}/node_modules/e2b"
printf '%s\n' 'serviceAccountJson is part of an installed SDK type definition' \
  >"${test_dir}/node_modules/e2b/index.d.ts"
"$guard" "$test_dir"

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

if [[ -f "$cloud_build_tf" ]]; then
  grep -F 'service-${data.google_project.current.number}@gcp-sa-cloudbuild.iam.gserviceaccount.com' \
    "$cloud_build_tf" >/dev/null
  grep -F 'resource "google_project_iam_member" "cloud_build_regional_service_agent"' \
    "$cloud_build_tf" >/dev/null
  grep -F 'resource "google_service_account_iam_member" "cloud_build_regional_image_builder_token_creator"' \
    "$cloud_build_tf" >/dev/null
  grep -F 'resource "google_storage_bucket_iam_member" "cloud_build_source_reader"' \
    "$cloud_build_tf" >/dev/null
fi

echo "Keyless GCP runtime guard tests passed."
