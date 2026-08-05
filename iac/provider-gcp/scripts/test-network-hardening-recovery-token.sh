#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
assertion_script="${script_dir}/assert-network-hardening-recovery-token.sh"
test_dir="$(mktemp -d)"
trap 'rm -rf "${test_dir}"' EXIT

repo="${test_dir}/repo"
mkdir -p "${repo}"
git -C "${repo}" init -q
git -C "${repo}" config user.name fixture
git -C "${repo}" config user.email fixture@example.invalid
printf 'fixture\n' >"${repo}/tracked"
git -C "${repo}" add tracked
git -C "${repo}" commit -qm fixture
head="$(git -C "${repo}" rev-parse HEAD)"
environment='dev'
state_prefix='terraform/orchestration/dev/state'

token="${test_dir}/token.json"
jq -n \
  --arg holder "cluster-apply:worker:${environment}:${state_prefix}:${head}:$(printf fixture | shasum -a 256 | awk '{print $1}')" '
    {
      schema_version: 1,
      uri: "gs://monad-code-terraform-state/operator-locks/monad-code/us-east4/workload-mutation.json",
      bucket: "monad-code-terraform-state",
      project: "monad-code",
      region: "us-east4",
      holder: $holder,
      generation: 42
    }
  ' >"${token}"
chmod 0600 "${token}"

expect_failure() {
  local name="$1"
  local expected="$2"
  shift 2
  local output="${test_dir}/${name}.output"

  if "$@" >"${output}" 2>&1; then
    printf 'Expected %s to fail.\n' "${name}" >&2
    exit 1
  fi
  grep -F "${expected}" "${output}" >/dev/null || {
    printf '%s failed for an unexpected reason:\n' "${name}" >&2
    sed -n '1,120p' "${output}" >&2
    exit 1
  }
}

"${assertion_script}" \
  "${token}" monad-code-terraform-state monad-code us-east4 worker \
  "${environment}" "${state_prefix}" "${repo}" >/dev/null

chmod 0644 "${token}"
expect_failure \
  public-mode \
  'Recovery token must be private' \
  "${assertion_script}" "${token}" monad-code-terraform-state \
  monad-code us-east4 worker "${environment}" "${state_prefix}" "${repo}"
chmod 0600 "${token}"

expect_failure \
  wrong-stage \
  'not bound to this canonical bucket, project, region, environment, backend prefix, stage, and exact source head' \
  "${assertion_script}" "${token}" monad-code-terraform-state \
  monad-code us-east4 api "${environment}" "${state_prefix}" "${repo}"

expect_failure \
  invalid-stage \
  'Invalid network-hardening recovery stage' \
  "${assertion_script}" "${token}" monad-code-terraform-state \
  monad-code us-east4 disabled "${environment}" "${state_prefix}" "${repo}"

expect_failure \
  wrong-environment \
  'not bound to this canonical bucket, project, region, environment, backend prefix, stage, and exact source head' \
  "${assertion_script}" "${token}" monad-code-terraform-state \
  monad-code us-east4 worker staging terraform/orchestration/staging/state "${repo}"

expect_failure \
  wrong-state-prefix \
  'not bound to this canonical bucket, project, region, environment, backend prefix, stage, and exact source head' \
  "${assertion_script}" "${token}" monad-code-terraform-state \
  monad-code us-east4 worker "${environment}" terraform/orchestration/other/state "${repo}"

jq '.generation = null' "${token}" >"${test_dir}/invalid-generation.json"
chmod 0600 "${test_dir}/invalid-generation.json"
expect_failure \
  invalid-generation \
  'not bound to this canonical bucket, project, region, environment, backend prefix, stage, and exact source head' \
  "${assertion_script}" "${test_dir}/invalid-generation.json" \
  monad-code-terraform-state monad-code us-east4 worker \
  "${environment}" "${state_prefix}" "${repo}"

jq '
  .bucket = "other-state-bucket"
  | .uri = "gs://other-state-bucket/operator-locks/monad-code/us-east4/workload-mutation.json"
' "${token}" >"${test_dir}/wrong-bucket.json"
chmod 0600 "${test_dir}/wrong-bucket.json"
expect_failure \
  wrong-bucket \
  'not bound to this canonical bucket, project, region, environment, backend prefix, stage, and exact source head' \
  "${assertion_script}" "${test_dir}/wrong-bucket.json" \
  monad-code-terraform-state monad-code us-east4 worker \
  "${environment}" "${state_prefix}" "${repo}"

jq '.uri = "gs://monad-code-terraform-state/operator-locks/monad-code/us-central1/workload-mutation.json"' \
  "${token}" >"${test_dir}/wrong-uri.json"
chmod 0600 "${test_dir}/wrong-uri.json"
expect_failure \
  wrong-uri \
  'not bound to this canonical bucket, project, region, environment, backend prefix, stage, and exact source head' \
  "${assertion_script}" "${test_dir}/wrong-uri.json" \
  monad-code-terraform-state monad-code us-east4 worker \
  "${environment}" "${state_prefix}" "${repo}"

ln -s "${token}" "${test_dir}/token-link.json"
expect_failure \
  symlink \
  'Recovery token must be a regular, non-symlink file' \
  "${assertion_script}" "${test_dir}/token-link.json" \
  monad-code-terraform-state monad-code us-east4 worker \
  "${environment}" "${state_prefix}" "${repo}"

printf 'Network-hardening recovery-token tests passed.\n'
