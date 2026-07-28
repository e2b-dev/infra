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
  for argument in "$@"; do
    case "${argument}" in
      --custom-metadata=monad-holder=*)
        holder="${argument#--custom-metadata=monad-holder=}"
        ;;
    esac
  done
  [[ ! -e "${object_path}" ]] || exit 1
  mkdir -p "$(dirname "${object_path}")"
  cp "${source_path}" "${object_path}"
  jq -cn \
    --arg holder "${holder}" \
    '{generation: "1", metadata: {"monad-holder": $holder}}' \
    >"${object_path}.meta"
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
holder="aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
uri_path="${FAKE_GCS_ROOT}/state-bucket/operator-locks/test-project/us-east4/workload-mutation.json"

"${lease_script}" acquire \
  "${fake_gcloud}" state-bucket test-project us-east4 "${holder}" "${token}"
[[ -f "${token}" ]]
[[ "$(stat -c '%a' "${token}" 2>/dev/null || stat -f '%Lp' "${token}")" == "600" ]]
[[ -f "${uri_path}" ]]

if "${lease_script}" acquire \
  "${fake_gcloud}" state-bucket test-project us-east4 second-holder \
  "${temp_dir}/second-token.json" >/dev/null 2>&1; then
  printf 'Concurrent lease acquisition unexpectedly succeeded.\n' >&2
  exit 1
fi

jq '.holder = "tampered-holder"' "${token}" >"${temp_dir}/tampered-token.json"
chmod 0600 "${temp_dir}/tampered-token.json"
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

"${lease_script}" release "${fake_gcloud}" "${token}"
[[ ! -e "${uri_path}" ]]

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
