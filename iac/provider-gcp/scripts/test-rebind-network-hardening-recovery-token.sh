#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
lease_script="${script_dir}/rollout-mutation-lease.sh"
rebind_script="${script_dir}/rebind-network-hardening-recovery-token.sh"
test_dir="$(mktemp -d "${TMPDIR:-/tmp}/network-rebind-test.XXXXXX")"
trap 'rm -rf -- "${test_dir}"' EXIT HUP INT TERM

fake_gcloud="${test_dir}/gcloud"
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
    current="$(jq -er '.generation | tostring' "${object_path}.meta")"
    [[ "${expected}" == "${current}" ]] || exit 1
    generation=$((current + 1))
  fi
  cp "${source_path}" "${object_path}"
  jq -cn \
    --arg generation "${generation}" \
    --arg holder "${holder}" \
    '{generation:$generation,custom_fields:{"monad-holder":$holder}}' \
    >"${object_path}.meta"
elif [[ "$1 $2 $3" == "storage objects describe" ]]; then
  object_path="$(uri_path "$4")"
  cat "${object_path}.meta"
elif [[ "$1 $2" == "storage rm" ]]; then
  object_path="$(uri_path "$3")"
  expected=""
  for argument in "$@"; do
    case "${argument}" in
      --if-generation-match=*) expected="${argument#--if-generation-match=}" ;;
    esac
  done
  [[ "${expected}" == "$(jq -er '.generation | tostring' "${object_path}.meta")" ]]
  rm -f -- "${object_path}" "${object_path}.meta"
else
  printf 'unexpected fake gcloud invocation: %s\n' "$*" >&2
  exit 1
fi
EOF
chmod 0755 "${fake_gcloud}"

expect_fail() {
  local description="$1"
  shift
  if "$@" >"${test_dir}/stdout" 2>"${test_dir}/stderr"; then
    printf 'expected failure: %s\n' "${description}" >&2
    exit 1
  fi
}

init_repo() {
  local repo="$1"
  mkdir -p "${repo}/docs"
  git -C "${repo}" init -q
  git -C "${repo}" config user.name fixture
  git -C "${repo}" config user.email fixture@example.invalid
  printf 'original\n' >"${repo}/docs/ARCHITECTURE.md"
  git -C "${repo}" add docs/ARCHITECTURE.md
  git -C "${repo}" commit -qm original
}

acquire_old_token() {
  local repo="$1"
  local token="$2"
  local source_head="$3"
  local digest="aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
  local holder="cluster-apply:network:dev:terraform/orchestration/dev/state:${source_head}:${digest}"
  "${lease_script}" acquire \
    "${fake_gcloud}" state-bucket test-project us-east4 \
    "${holder}" "${token}" >/dev/null
}

export FAKE_GCS_ROOT="${test_dir}/positive-gcs"
positive_repo="${test_dir}/positive-repo"
mkdir -p "${positive_repo}"
init_repo "${positive_repo}"
old_head="$(git -C "${positive_repo}" rev-parse HEAD)"
printf 'reviewed network repair\n' >>"${positive_repo}/docs/ARCHITECTURE.md"
git -C "${positive_repo}" add docs/ARCHITECTURE.md
git -C "${positive_repo}" commit -qm repair
new_head="$(git -C "${positive_repo}" rev-parse HEAD)"
old_token="${test_dir}/old-token.json"
new_token="${test_dir}/new-token.json"
acquire_old_token "${positive_repo}" "${old_token}" "${old_head}"
"${rebind_script}" \
  "${old_token}" "${new_token}" state-bucket test-project us-east4 \
  network dev terraform/orchestration/dev/state \
  "${positive_repo}" "${fake_gcloud}" >/dev/null
[[ ! -e "${old_token}" && -f "${new_token}" ]]
[[ "$(jq -er '.generation | tostring' "${new_token}")" == "2" ]]
[[ "$(jq -er '.holder' "${new_token}")" == \
  cluster-apply:network:dev:terraform/orchestration/dev/state:"${new_head}":* ]]
"${lease_script}" assert-held \
  "${fake_gcloud}" state-bucket test-project us-east4 "${new_token}" >/dev/null
"${lease_script}" release "${fake_gcloud}" "${new_token}" >/dev/null

export FAKE_GCS_ROOT="${test_dir}/unrelated-gcs"
unrelated_repo="${test_dir}/unrelated-repo"
mkdir -p "${unrelated_repo}"
init_repo "${unrelated_repo}"
unrelated_old_head="$(git -C "${unrelated_repo}" rev-parse HEAD)"
printf 'unrelated\n' >"${unrelated_repo}/other.txt"
git -C "${unrelated_repo}" add other.txt
git -C "${unrelated_repo}" commit -qm unrelated
unrelated_token="${test_dir}/unrelated-token.json"
acquire_old_token "${unrelated_repo}" "${unrelated_token}" "${unrelated_old_head}"
expect_fail "unrelated descendant cannot inherit the recovery lease" \
  "${rebind_script}" \
  "${unrelated_token}" "${test_dir}/unrelated-new.json" \
  state-bucket test-project us-east4 network dev \
  terraform/orchestration/dev/state "${unrelated_repo}" "${fake_gcloud}"
"${lease_script}" assert-held \
  "${fake_gcloud}" state-bucket test-project us-east4 "${unrelated_token}" >/dev/null
"${lease_script}" release "${fake_gcloud}" "${unrelated_token}" >/dev/null

export FAKE_GCS_ROOT="${test_dir}/dirty-gcs"
dirty_repo="${test_dir}/dirty-repo"
mkdir -p "${dirty_repo}"
init_repo "${dirty_repo}"
dirty_old_head="$(git -C "${dirty_repo}" rev-parse HEAD)"
printf 'reviewed\n' >>"${dirty_repo}/docs/ARCHITECTURE.md"
git -C "${dirty_repo}" add docs/ARCHITECTURE.md
git -C "${dirty_repo}" commit -qm repair
printf 'uncommitted\n' >>"${dirty_repo}/docs/ARCHITECTURE.md"
dirty_token="${test_dir}/dirty-token.json"
acquire_old_token "${dirty_repo}" "${dirty_token}" "${dirty_old_head}"
expect_fail "dirty source cannot inherit the recovery lease" \
  "${rebind_script}" \
  "${dirty_token}" "${test_dir}/dirty-new.json" \
  state-bucket test-project us-east4 network dev \
  terraform/orchestration/dev/state "${dirty_repo}" "${fake_gcloud}"
"${lease_script}" assert-held \
  "${fake_gcloud}" state-bucket test-project us-east4 "${dirty_token}" >/dev/null
"${lease_script}" release "${fake_gcloud}" "${dirty_token}" >/dev/null

export FAKE_GCS_ROOT="${test_dir}/same-gcs"
same_repo="${test_dir}/same-repo"
mkdir -p "${same_repo}"
init_repo "${same_repo}"
same_head="$(git -C "${same_repo}" rev-parse HEAD)"
same_token="${test_dir}/same-token.json"
acquire_old_token "${same_repo}" "${same_token}" "${same_head}"
expect_fail "same source head does not need a rebind" \
  "${rebind_script}" \
  "${same_token}" "${test_dir}/same-new.json" \
  state-bucket test-project us-east4 network dev \
  terraform/orchestration/dev/state "${same_repo}" "${fake_gcloud}"
"${lease_script}" release "${fake_gcloud}" "${same_token}" >/dev/null

printf 'Network-hardening recovery source rebind guards passed.\n'
