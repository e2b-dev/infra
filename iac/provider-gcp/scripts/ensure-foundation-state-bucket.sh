#!/usr/bin/env bash
set -euo pipefail

bucket_name="${1:?usage: ensure-foundation-state-bucket.sh BUCKET PROJECT REGION [GCLOUD_BIN]}"
project_id="${2:?usage: ensure-foundation-state-bucket.sh BUCKET PROJECT REGION [GCLOUD_BIN]}"
expected_location="${3:?usage: ensure-foundation-state-bucket.sh BUCKET PROJECT REGION [GCLOUD_BIN]}"
gcloud_bin="${4:-gcloud}"
soft_delete_seconds="2592000"

command -v jq >/dev/null 2>&1 || {
  printf 'jq is required to verify the Terraform state bucket.\n' >&2
  exit 1
}
if [[ ! -x "${gcloud_bin}" ]] && ! command -v "${gcloud_bin}" >/dev/null 2>&1; then
  printf 'gcloud is not installed or not executable: %s\n' "${gcloud_bin}" >&2
  exit 1
fi

umask 077
bucket_json="$(mktemp)"
describe_error="$(mktemp)"
trap 'rm -f "${bucket_json}" "${describe_error}"' EXIT

project_number="$(
  "${gcloud_bin}" projects describe "${project_id}" \
    --format='value(projectNumber)'
)"
if [[ -z "${project_number}" ]]; then
  printf 'Unable to resolve the numeric project identity for %s.\n' "${project_id}" >&2
  exit 1
fi

describe_bucket() {
  "${gcloud_bin}" storage buckets describe "gs://${bucket_name}" \
    --raw \
    --format=json
}

assert_bucket_identity() {
  if ! jq -e \
    --arg name "${bucket_name}" \
    --arg project_number "${project_number}" \
    --arg location "${expected_location}" \
    '
      .name == $name
      and ((.projectNumber | tostring) == $project_number)
      and ((.location | ascii_upcase) == ($location | ascii_upcase))
    ' "${bucket_json}" >/dev/null; then
    printf 'Refusing Terraform state bucket with unexpected immutable identity.\n' >&2
    jq -r '
      "Observed: name=\(.name // "<missing>") projectNumber=\(.projectNumber // "<missing>") location=\(.location // "<missing>")"
    ' "${bucket_json}" >&2 || true
    exit 1
  fi
}

if ! describe_bucket >"${bucket_json}" 2>"${describe_error}"; then
  if ! "${gcloud_bin}" storage buckets create "gs://${bucket_name}" \
    --project="${project_id}" \
    --location="${expected_location}" \
    --default-storage-class=STANDARD \
    --uniform-bucket-level-access \
    --public-access-prevention \
    --soft-delete-duration=30d \
    --quiet; then
    printf 'State bucket was not readable and creation failed. Initial describe error:\n' >&2
    cat "${describe_error}" >&2
    exit 1
  fi
  describe_bucket >"${bucket_json}"
fi

# Verify immutable ownership and location before changing an existing bucket.
assert_bucket_identity

"${gcloud_bin}" storage buckets update "gs://${bucket_name}" \
  --project="${project_id}" \
  --default-storage-class=STANDARD \
  --uniform-bucket-level-access \
  --public-access-prevention \
  --versioning \
  --soft-delete-duration=30d \
  --quiet

describe_bucket >"${bucket_json}"
assert_bucket_identity

if ! jq -e \
  --arg soft_delete_seconds "${soft_delete_seconds}" \
  '
    .storageClass == "STANDARD"
    and (.iamConfiguration.uniformBucketLevelAccess.enabled == true)
    and (.iamConfiguration.publicAccessPrevention == "enforced")
    and (.versioning.enabled == true)
    and ((.softDeletePolicy.retentionDurationSeconds | tostring) == $soft_delete_seconds)
  ' "${bucket_json}" >/dev/null; then
  printf 'Terraform state bucket does not satisfy required security and recovery controls.\n' >&2
  jq -r '
    "Observed: storageClass=\(.storageClass // "<missing>") uniformAccess=\(.iamConfiguration.uniformBucketLevelAccess.enabled // "<missing>") publicAccessPrevention=\(.iamConfiguration.publicAccessPrevention // "<missing>") versioning=\(.versioning.enabled // "<missing>") softDeleteSeconds=\(.softDeletePolicy.retentionDurationSeconds // "<missing>")"
  ' "${bucket_json}" >&2 || true
  exit 1
fi

printf 'Terraform state bucket verified: gs://%s (%s, project %s).\n' \
  "${bucket_name}" "${expected_location}" "${project_id}"
