#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
lease_script="${script_dir}/rollout-mutation-lease.sh"
temp_dir="$(mktemp -d "${TMPDIR:-/tmp}/rollout-lease-test.XXXXXX")"
trap 'rm -rf -- "${temp_dir}"' EXIT HUP INT TERM

fake_gcloud="${temp_dir}/gcloud"
cat >"${fake_gcloud}" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

uri_path() {
  printf '%s/%s' "${FAKE_GCS_ROOT}" "${1#gs://}"
}

if [[ "$1 $2" == "storage cp" ]]; then
  source_path="$3"
  uri="$4"
  object_path="$(uri_path "${uri}")"
  holder=""
  expected=""
  for argument in "$@"; do
    case "${argument}" in
      --custom-metadata=monad-holder=*)
        holder="${argument#--custom-metadata=monad-holder=}"
        ;;
      --if-generation-match=*)
        expected="${argument#--if-generation-match=}"
        ;;
    esac
  done
  mkdir -p "$(dirname "${object_path}")"
  if [[ "${expected}" == "0" ]]; then
    [[ ! -e "${object_path}" ]] || exit 1
    generation=1
  else
    [[ -f "${object_path}" && -f "${object_path}.meta" ]] || exit 1
    current_generation="$(jq -er '.generation | tostring' "${object_path}.meta")"
    [[ "${expected}" == "${current_generation}" ]] || exit 1
    generation=$((current_generation + 1))
  fi
  cp "${source_path}" "${object_path}"
  jq -cn \
    --arg generation "${generation}" \
    --arg holder "${holder}" \
    '{generation: $generation, custom_fields: {"monad-holder": $holder}}' \
    >"${object_path}.meta"
  if [[ "${expected}" != "0" \
    && "${FAKE_COMMIT_TRANSFER_THEN_FAIL:-false}" == "true" ]]; then
    exit 1
  fi
elif [[ "$1 $2 $3" == "storage objects describe" ]]; then
  [[ "${FAKE_FAIL_DESCRIBE:-false}" != "true" ]] || exit 1
  object_path="$(uri_path "$4")"
  cat "${object_path}.meta"
elif [[ "$1 $2" == "storage rm" ]]; then
  uri="$3"
  object_path="$(uri_path "${uri}")"
  expected=""
  for argument in "$@"; do
    case "${argument}" in
      --if-generation-match=*)
        expected="${argument#--if-generation-match=}"
        ;;
    esac
  done
  actual="$(jq -er '.generation | tostring' "${object_path}.meta")"
  [[ "${expected}" == "${actual}" ]] || exit 1
  rm -f -- "${object_path}" "${object_path}.meta"
else
  printf 'unexpected fake gcloud invocation: %s\n' "$*" >&2
  exit 1
fi
EOF
chmod 0755 "${fake_gcloud}"

export FAKE_GCS_ROOT="${temp_dir}/gcs"
token="${temp_dir}/lease-token.json"
holder="cluster-apply:worker:dev:terraform/orchestration/dev/state:0123456789abcdef0123456789abcdef01234567:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
uri_path="${FAKE_GCS_ROOT}/state-bucket/operator-locks/test-project/us-east4/workload-mutation.json"

"${lease_script}" acquire \
  "${fake_gcloud}" state-bucket test-project us-east4 "${holder}" "${token}"
[[ -f "${token}" ]]
[[ "$(stat -c '%a' "${token}" 2>/dev/null || stat -f '%Lp' "${token}")" == "600" ]]
[[ -f "${uri_path}" ]]

"${lease_script}" assert-held \
  "${fake_gcloud}" state-bucket test-project us-east4 "${token}" >/dev/null
[[ -f "${token}" && -f "${uri_path}" ]]

if "${lease_script}" assert-held \
  "${fake_gcloud}" other-state-bucket test-project us-east4 "${token}" \
  >/dev/null 2>&1; then
  printf 'Wrong expected bucket unexpectedly validated the live lease.\n' >&2
  exit 1
fi

jq '
  .bucket = "other-state-bucket"
  | .uri = "gs://other-state-bucket/operator-locks/test-project/us-east4/workload-mutation.json"
' "${token}" >"${temp_dir}/wrong-bucket-token.json"
chmod 0600 "${temp_dir}/wrong-bucket-token.json"
if "${lease_script}" assert-held \
  "${fake_gcloud}" state-bucket test-project us-east4 \
  "${temp_dir}/wrong-bucket-token.json" >/dev/null 2>&1; then
  printf 'Self-consistent wrong-bucket token unexpectedly validated the canonical lease.\n' >&2
  exit 1
fi

jq '.generation = "2"' "${uri_path}.meta" >"${temp_dir}/new-generation-meta.json"
mv "${temp_dir}/new-generation-meta.json" "${uri_path}.meta"
if "${lease_script}" assert-held \
  "${fake_gcloud}" state-bucket test-project us-east4 "${token}" \
  >/dev/null 2>&1; then
  printf 'Stale-generation token unexpectedly validated the replaced lease.\n' >&2
  exit 1
fi
[[ -f "${token}" && -f "${uri_path}" ]]
jq '.generation = "1"' "${uri_path}.meta" >"${temp_dir}/original-generation-meta.json"
mv "${temp_dir}/original-generation-meta.json" "${uri_path}.meta"

jq '.custom_fields["monad-holder"] = "replacement-holder"' \
  "${uri_path}.meta" >"${temp_dir}/replacement-holder-meta.json"
mv "${temp_dir}/replacement-holder-meta.json" "${uri_path}.meta"
if "${lease_script}" assert-held \
  "${fake_gcloud}" state-bucket test-project us-east4 "${token}" \
  >/dev/null 2>&1; then
  printf 'Replaced-holder token unexpectedly validated the live lease.\n' >&2
  exit 1
fi
jq --arg holder "${holder}" '.custom_fields["monad-holder"] = $holder' \
  "${uri_path}.meta" >"${temp_dir}/original-holder-meta.json"
mv "${temp_dir}/original-holder-meta.json" "${uri_path}.meta"

if "${lease_script}" acquire \
  "${fake_gcloud}" state-bucket test-project us-east4 second-holder \
  "${temp_dir}/second-token.json" >/dev/null 2>&1; then
  printf 'Concurrent lease acquisition unexpectedly succeeded.\n' >&2
  exit 1
fi

jq '.holder = "tampered-holder"' "${token}" >"${temp_dir}/tampered-token.json"
chmod 0600 "${temp_dir}/tampered-token.json"
if "${lease_script}" assert-held \
  "${fake_gcloud}" state-bucket test-project us-east4 \
  "${temp_dir}/tampered-token.json" >/dev/null 2>&1; then
  printf 'Tampered holder unexpectedly validated the live lease.\n' >&2
  exit 1
fi
if "${lease_script}" release \
  "${fake_gcloud}" "${temp_dir}/tampered-token.json" >/dev/null 2>&1; then
  printf 'Tampered holder unexpectedly released the lease.\n' >&2
  exit 1
fi
[[ -f "${uri_path}" ]]

cp "${token}" "${temp_dir}/public-token.json"
chmod 0644 "${temp_dir}/public-token.json"
if "${lease_script}" release \
  "${fake_gcloud}" "${temp_dir}/public-token.json" >/dev/null 2>&1; then
  printf 'Public lease token unexpectedly released the lease.\n' >&2
  exit 1
fi
[[ -f "${uri_path}" ]]

jq '.metadata = {"monad-holder": "conflicting-holder"}' \
  "${uri_path}.meta" >"${temp_dir}/conflicting-meta.json"
mv "${temp_dir}/conflicting-meta.json" "${uri_path}.meta"
if "${lease_script}" release \
  "${fake_gcloud}" "${token}" >/dev/null 2>&1; then
  printf 'Conflicting custom metadata unexpectedly released the lease.\n' >&2
  exit 1
fi
[[ -f "${uri_path}" ]]
jq 'del(.metadata)' "${uri_path}.meta" >"${temp_dir}/restored-meta.json"
mv "${temp_dir}/restored-meta.json" "${uri_path}.meta"

stale_token="${temp_dir}/stale-transfer-token.json"
cp "${token}" "${stale_token}"
chmod 0600 "${stale_token}"
replacement_token="${temp_dir}/replacement-token.json"
replacement_holder="cluster-apply:network:dev:terraform/orchestration/dev/state:abcdef0123456789abcdef0123456789abcdef01:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
"${lease_script}" transfer \
  "${fake_gcloud}" "${token}" "${replacement_holder}" "${replacement_token}"
[[ ! -e "${token}" && -f "${replacement_token}" ]]
[[ "$(jq -er '.generation | tostring' "${replacement_token}")" == "2" ]]
[[ "$(jq -er '.holder' "${replacement_token}")" == "${replacement_holder}" ]]
"${lease_script}" assert-held \
  "${fake_gcloud}" state-bucket test-project us-east4 "${replacement_token}" >/dev/null
if "${lease_script}" assert-held \
  "${fake_gcloud}" state-bucket test-project us-east4 "${stale_token}" \
  >/dev/null 2>&1; then
  printf 'Pre-transfer token unexpectedly validated the replacement lease.\n' >&2
  exit 1
fi
if "${lease_script}" transfer \
  "${fake_gcloud}" "${stale_token}" later-holder \
  "${temp_dir}/later-token.json" >/dev/null 2>&1; then
  printf 'Stale generation unexpectedly transferred the live lease.\n' >&2
  exit 1
fi

"${lease_script}" release "${fake_gcloud}" "${replacement_token}"
[[ ! -e "${uri_path}" ]]

ambiguous_token="${temp_dir}/ambiguous-token.json"
ambiguous_new_token="${temp_dir}/ambiguous-new-token.json"
ambiguous_holder="cluster-apply:network:dev:terraform/orchestration/dev/state:1111111111111111111111111111111111111111:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
"${lease_script}" acquire \
  "${fake_gcloud}" state-bucket test-project us-east4 \
  ambiguous-original-holder "${ambiguous_token}" >/dev/null
export FAKE_COMMIT_TRANSFER_THEN_FAIL=true
if "${lease_script}" transfer \
  "${fake_gcloud}" "${ambiguous_token}" "${ambiguous_holder}" \
  "${ambiguous_new_token}" >"${temp_dir}/ambiguous.stdout" \
  2>"${temp_dir}/ambiguous.stderr"; then
  printf 'Ambiguous transfer response unexpectedly reported success.\n' >&2
  exit 1
fi
unset FAKE_COMMIT_TRANSFER_THEN_FAIL
grep -F 'canonical object may still have changed' \
  "${temp_dir}/ambiguous.stderr" >/dev/null
[[ -f "${ambiguous_token}" && ! -e "${ambiguous_new_token}" ]]
[[ "$(jq -er '.generation | tostring' "${uri_path}.meta")" == "2" ]]
[[ "$(jq -er '.custom_fields["monad-holder"]' "${uri_path}.meta")" == \
  "${ambiguous_holder}" ]]
if "${lease_script}" assert-held \
  "${fake_gcloud}" state-bucket test-project us-east4 "${ambiguous_token}" \
  >/dev/null 2>&1; then
  printf 'Original token unexpectedly validated after an ambiguous committed transfer.\n' >&2
  exit 1
fi
"${fake_gcloud}" storage rm \
  "gs://state-bucket/operator-locks/test-project/us-east4/workload-mutation.json" \
  --if-generation-match=2 >/dev/null

export FAKE_FAIL_DESCRIBE=true
if "${lease_script}" acquire \
  "${fake_gcloud}" state-bucket test-project us-east4 stranded-holder \
  "${temp_dir}/stranded-token.json" >/dev/null 2>&1; then
  printf 'Acquire unexpectedly succeeded when generation capture failed.\n' >&2
  exit 1
fi
[[ -f "${uri_path}" ]]
[[ ! -e "${temp_dir}/stranded-token.json" ]]

printf 'Shared rollout mutation lease guards passed.\n'
