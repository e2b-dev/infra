#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
workspace="$(mktemp -d)"
trap 'rm -rf -- "${workspace}"' EXIT

fake_terraform="${workspace}/terraform"
state_file="${workspace}/state"
command_log="${workspace}/commands"

cat >"${fake_terraform}" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

printf '%q ' "$@" >>"${COMMAND_LOG}"
printf '\n' >>"${COMMAND_LOG}"

[[ "${1:-}" == "state" ]] || exit 2
case "${2:-}" in
  list)
    if [[ -f "${STATE_FILE}.error" ]]; then
      printf 'backend unavailable\n' >&2
      exit 1
    fi
    if [[ -f "${STATE_FILE}.missing" ]]; then
      printf 'No state file was found!\n' >&2
      exit 1
    fi
    cat "${STATE_FILE}"
    ;;
  rm)
    shift 2
    while [[ "${1:-}" == -* ]]; do
      shift
    done
    temp_state="${STATE_FILE}.next"
    cp "${STATE_FILE}" "${temp_state}"
    for address in "$@"; do
      grep -Fvx "${address}" "${temp_state}" >"${temp_state}.filtered" || true
      mv "${temp_state}.filtered" "${temp_state}"
      printf 'Removed %s\n' "${address}"
    done
    mv "${temp_state}" "${STATE_FILE}"
    ;;
  *)
    exit 2
    ;;
esac
EOF
chmod +x "${fake_terraform}"

export STATE_FILE="${state_file}"
export COMMAND_LOG="${command_log}"

expect_pass() {
  local description="$1"
  shift
  if ! "$@" >"${workspace}/stdout" 2>"${workspace}/stderr"; then
    printf 'expected pass: %s\n' "${description}" >&2
    cat "${workspace}/stderr" >&2
    exit 1
  fi
}

expect_fail() {
  local description="$1"
  shift
  if "$@" >"${workspace}/stdout" 2>"${workspace}/stderr"; then
    printf 'expected failure: %s\n' "${description}" >&2
    exit 1
  fi
}

: >"${state_file}"
: >"${command_log}"
touch "${state_file}.missing"
expect_pass \
  "missing state needs no scrub" \
  "${script_dir}/scrub-legacy-acl-token-state.sh" \
  "${fake_terraform}"
rm "${state_file}.missing"

printf '%s\n' \
  "module.init.google_project_service.compute_engine_api" \
  "module.init.random_uuid.consul_acl_token" \
  "module.init.random_uuid.nomad_acl_token" \
  >"${state_file}"
: >"${command_log}"
expect_fail \
  "refresh guard blocks leaking ACL generator state" \
  "${script_dir}/assert-no-legacy-acl-token-state.sh" \
  "${fake_terraform}"
expect_pass \
  "exact legacy ACL generators are forgotten" \
  "${script_dir}/scrub-legacy-acl-token-state.sh" \
  "${fake_terraform}"
if grep -Eq 'random_uuid\.(consul_acl_token|nomad_acl_token)$' "${state_file}"; then
  printf 'legacy ACL generator remained in fake state\n' >&2
  exit 1
fi
expect_pass \
  "refresh guard passes after scrub" \
  "${script_dir}/assert-no-legacy-acl-token-state.sh" \
  "${fake_terraform}"
grep -Fq -- \
  "state rm -lock-timeout=5m module.init.random_uuid.consul_acl_token module.init.random_uuid.nomad_acl_token" \
  "${command_log}"

: >"${command_log}"
expect_pass \
  "already scrubbed state is idempotent" \
  "${script_dir}/scrub-legacy-acl-token-state.sh" \
  "${fake_terraform}"
if grep -Fq -- "state rm" "${command_log}"; then
  printf 'idempotent scrub unexpectedly mutated state\n' >&2
  exit 1
fi

printf '%s\n' \
  "module.init.random_uuid.consul_acl_token" \
  "module.cluster.google_compute_instance.server" \
  >"${state_file}"
expect_fail \
  "non-foundation state is refused" \
  "${script_dir}/scrub-legacy-acl-token-state.sh" \
  "${fake_terraform}"

printf '%s\n' \
  "module.init.google_project_service.compute_engine_api" \
  "random_uuid.consul_acl_token" \
  >"${state_file}"
expect_fail \
  "unexpected ACL generator address is refused" \
  "${script_dir}/scrub-legacy-acl-token-state.sh" \
  "${fake_terraform}"

rm -f "${state_file}.missing"
touch "${state_file}.error"
expect_fail \
  "backend inspection failure cannot mutate state" \
  "${script_dir}/scrub-legacy-acl-token-state.sh" \
  "${fake_terraform}"

printf 'Legacy ACL token state scrub fixtures passed.\n'
