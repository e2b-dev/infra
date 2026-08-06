#!/usr/bin/env bash
set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
nomad_script="${root_dir}/nomad-cluster/scripts/run-nomad.sh"
consul_script="${root_dir}/nomad-cluster/scripts/run-consul.sh"
startup_scripts=(
  "${root_dir}/modules/nodepool-api/scripts/start-api.sh"
  "${root_dir}/nomad-cluster/scripts/start-clickhouse.sh"
  "${root_dir}/nomad-cluster/scripts/start-server.sh"
)
work_dir="$(mktemp -d)"
trap 'rm -rf -- "${work_dir}"' EXIT

if grep -Eq '^[[:space:]]*set[[:space:]]+-x([[:space:]]|$)' \
  "${nomad_script}" "${consul_script}" "${startup_scripts[@]}"; then
  printf 'Control-plane bootstrap scripts must not enable shell tracing.\n' >&2
  exit 1
fi

mkdir -p "${work_dir}/bin"
cat >"${work_dir}/bin/curl" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' '{"server":{"message":"ok","ok":true}}'
EOF
cat >"${work_dir}/bin/nomad" <<'EOF'
#!/usr/bin/env bash
test "${1:-}" = "acl"
test "${2:-}" = "bootstrap"
test "${3:-}" = "${NOMAD_TEST_BOOTSTRAP_TOKEN_FILE}"
grep -Fxq 'nomad-token-must-not-appear' "${3}"
if [[ "${NOMAD_STUB_MODE:-already}" == "unexpected" ]]; then
  printf '%s\n' 'permission denied' >&2
  exit 2
fi
printf '%s\n' 'Unexpected response code: 400 (ACL bootstrap already done (reset index: 7))' >&2
exit 1
EOF
chmod +x "${work_dir}/bin/curl" "${work_dir}/bin/nomad"

sentinel='nomad-token-must-not-appear'
bootstrap_token_file="${work_dir}/bootstrap-token"
printf '%s\n' "${sentinel}" >"${bootstrap_token_file}"
chmod 0600 "${bootstrap_token_file}"
bootstrap_output="$(
  PATH="${work_dir}/bin:${PATH}" TMPDIR="${work_dir}" \
    NOMAD_TEST_BOOTSTRAP_TOKEN_FILE="${bootstrap_token_file}" bash -c '
    source "$1"
    bootstrap "$2"
  ' bash "${nomad_script}" "${bootstrap_token_file}" 2>&1
)"

if grep -Fq "${sentinel}" <<<"${bootstrap_output}"; then
  printf 'Nomad ACL bootstrap exposed its token in output.\n' >&2
  exit 1
fi
grep -Fq 'Nomad ACL is already bootstrapped' <<<"${bootstrap_output}"

if find "${work_dir}" -maxdepth 1 -name 'nomad.token.*' -print -quit | grep -q .; then
  printf 'Nomad ACL bootstrap left a token file behind.\n' >&2
  exit 1
fi

if unexpected_output="$(
  PATH="${work_dir}/bin:${PATH}" TMPDIR="${work_dir}" \
    NOMAD_TEST_BOOTSTRAP_TOKEN_FILE="${bootstrap_token_file}" \
    NOMAD_STUB_MODE=unexpected bash -c '
    source "$1"
    bootstrap "$2"
  ' bash "${nomad_script}" "${bootstrap_token_file}" 2>&1
)"; then
  printf 'Unexpected Nomad ACL bootstrap failures must remain fatal.\n' >&2
  exit 1
fi
if grep -Fq "${sentinel}" <<<"${unexpected_output}"; then
  printf 'Failed Nomad ACL bootstrap exposed its token in output.\n' >&2
  exit 1
fi
grep -Fq 'Nomad ACL bootstrap failed' <<<"${unexpected_output}"
if find "${work_dir}" -maxdepth 1 -name 'nomad.token.*' -print -quit | grep -q .; then
  printf 'Failed Nomad ACL bootstrap left a token file behind.\n' >&2
  exit 1
fi

printf 'Control-plane secret logging regression test passed.\n'
