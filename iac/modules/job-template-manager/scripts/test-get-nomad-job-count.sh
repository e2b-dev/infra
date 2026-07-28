#!/usr/bin/env bash

set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
subject="${script_dir}/get-nomad-job-count.sh"
fixture_dir="$(mktemp -d)"
trap 'rm -rf -- "${fixture_dir}"' EXIT HUP INT TERM

cat >"${fixture_dir}/curl" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "${FAKE_CURL_BODY:-}"
printf '%s\n' "---HTTP_STATUS:${FAKE_CURL_STATUS:-200}"
exit "${FAKE_CURL_EXIT:-0}"
EOF
chmod 0755 "${fixture_dir}/curl"

query='{"nomad_addr":"https://nomad.example.test","nomad_token":"redacted","job_name":"template-manager","min_count":"2"}'

run_success() {
  expected="${1:?expected count required}"
  body="${2:-}"
  status="${3:-200}"

  output="$(
    PATH="${fixture_dir}:${PATH}" \
      FAKE_CURL_BODY="${body}" \
      FAKE_CURL_STATUS="${status}" \
      "${subject}" <<<"${query}"
  )"
  test "$(jq -r '.count' <<<"${output}")" = "${expected}"
}

run_failure() {
  expected_message="${1:?expected message required}"
  body="${2:-}"
  status="${3:-200}"
  curl_exit="${4:-0}"
  stderr="${fixture_dir}/stderr"

  if PATH="${fixture_dir}:${PATH}" \
    FAKE_CURL_BODY="${body}" \
    FAKE_CURL_STATUS="${status}" \
    FAKE_CURL_EXIT="${curl_exit}" \
    "${subject}" <<<"${query}" >"${fixture_dir}/stdout" 2>"${stderr}"; then
    printf 'Expected template-manager count lookup to fail.\n' >&2
    exit 1
  fi
  grep -F "${expected_message}" "${stderr}" >/dev/null
}

run_success 2 '' 404
run_success 5 '{"TaskGroups":[{"Count":5}]}' 200
run_success 2 '{"TaskGroups":[{"Count":1}]}' 200

run_failure 'curl exit code: 6' 'name resolution failed' 000 6
run_failure 'HTTP 403' '{"error":"forbidden"}' 403
run_failure 'HTTP 500' '{"error":"unavailable"}' 500
run_failure 'Failed to parse job count' '{"TaskGroups":[]}' 200

printf 'Template-manager Nomad count lookup tests passed.\n'
