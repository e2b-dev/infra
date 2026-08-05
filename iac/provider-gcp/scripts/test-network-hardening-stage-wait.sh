#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
test_dir="$(mktemp -d)"
trap 'rm -rf "${test_dir}"' EXIT

fake_gcloud="${test_dir}/gcloud"
# These are literal lines in the generated fixture, not parent-shell expansions.
# shellcheck disable=SC2016
printf '%s\n' \
  '#!/usr/bin/env bash' \
  'set -euo pipefail' \
  'printf "%s\n" "$*" >>"${FAKE_GCLOUD_LOG:?}"' \
  'if [[ "${1:-}" == "secrets" && "${2:-}" == "versions" && "${3:-}" == "access" ]]; then' \
  '  printf "%s\n" "fixture-nomad-token"' \
  '  exit 0' \
  'fi' \
  '[[ "${1:-}" == "compute" ]] || exit 2' \
  'case "${2:-}" in' \
  '  firewall-rules)' \
  '    name="${4:?}"' \
  '    if [[ "${name}" == *internal-remote-connection-firewall-ingress ]]; then' \
  '      printf "%s\n" "{\"direction\":\"INGRESS\",\"disabled\":false,\"priority\":900,\"sourceRanges\":[\"35.235.240.0/20\"],\"targetTags\":[\"orch\"],\"allowed\":[{\"IPProtocol\":\"tcp\",\"ports\":[\"22\",\"3389\"]}],\"logConfig\":{\"enable\":true,\"metadata\":\"EXCLUDE_ALL_METADATA\"}}"' \
  '    else' \
  '      printf "%s\n" "{\"direction\":\"INGRESS\",\"disabled\":false,\"priority\":1000,\"sourceRanges\":[\"0.0.0.0/0\"],\"targetTags\":[\"orch\"],\"denied\":[{\"IPProtocol\":\"tcp\",\"ports\":[\"22\",\"3389\"]}],\"logConfig\":{\"enable\":true,\"metadata\":\"EXCLUDE_ALL_METADATA\"}}"' \
  '    fi' \
  '    ;;' \
  '  instance-groups)' \
  '    [[ "${3:-}" == "managed" ]] || exit 2' \
  '    action="${4:-}"' \
  '    group="${5:-}"' \
  '    case "${group}" in' \
  '      e2b-orch-server-rig) size=3; base_name=e2b-orch-server; base_id=1000 ;;' \
  '      e2b-orch-api-ig) size=2; base_name=e2b-orch-api; base_id=2000 ;;' \
  '      e2b-orch-client-rig) size=2; base_name=e2b-orch-client; base_id=3000 ;;' \
  '      e2b-orch-build-default-rig) size=1; base_name=e2b-orch-build-default; base_id=4000 ;;' \
  '      e2b-orch-loki-ig) size=0; base_name=e2b-orch-loki; base_id=5000 ;;' \
  '      e2b-clickhouse-ig) size=0; base_name=e2b-clickhouse; base_id=6000 ;;' \
  '      *) exit 2 ;;' \
  '    esac' \
  '    mode="${FAKE_GCLOUD_MODE:-stable}"' \
  '    if [[ "${action}" == "describe" ]]; then' \
  '      target_template="https://www.googleapis.com/compute/v1/projects/monad-code/global/instanceTemplates/${base_name}-template-v2"' \
  '      count="$(<"${FAKE_GCLOUD_COUNTER:?}")"' \
  '      count=$((count + 1))' \
  '      printf "%s\n" "${count}" >"${FAKE_GCLOUD_COUNTER}"' \
  '      if [[ "${mode}" == "delayed" && "${count}" -gt 1 ]]; then mode=stable; fi' \
  '      if [[ "${mode}" == "unstable" || "${mode}" == "delayed" ]]; then' \
  '        jq -cn --argjson size "${size}" --arg template "${target_template}" "{targetSize:\$size,versions:[{instanceTemplate:\$template}],status:{isStable:false,versionTarget:{isReached:false}},currentActions:{none:0,recreating:\$size}}"' \
  '      elif [[ "${mode}" == "missing-version" ]]; then' \
  '        jq -cn --argjson size "${size}" --arg template "${target_template}" "{targetSize:\$size,versions:[{instanceTemplate:\$template}],status:{isStable:true},currentActions:{none:\$size}}"' \
  '      else' \
  '        jq -cn --argjson size "${size}" --arg template "${target_template}" "{targetSize:\$size,versions:[{instanceTemplate:\$template}],status:{isStable:true,versionTarget:{isReached:true}},currentActions:{none:\$size}}"' \
  '      fi' \
  '      exit 0' \
  '    fi' \
  '    [[ "${action}" == "list-instances" ]] || exit 2' \
  '    instances="[]"' \
  '    for ((index=1; index<=size; index++)); do' \
  '      name="${base_name}-${index}"' \
  '      id="$((base_id + index))"' \
  '      if [[ "${mode}" == "identity-race" && "${group}" == "e2b-orch-client-rig" && "${index}" -eq 1 ]]; then id=9999; fi' \
  '      if [[ "${mode}" == "identity-change" && "${group}" == "e2b-orch-client-rig" && "${index}" -eq 1 && "$(<"${FAKE_GCLOUD_COUNTER:?}")" -gt 1 ]]; then id=9999; fi' \
  '      health=HEALTHY' \
  '      if [[ "${mode}" == "inventory-unhealthy" && "${index}" -eq 1 ]]; then health=UNHEALTHY; fi' \
  '      instance="https://www.googleapis.com/compute/v1/projects/monad-code/zones/us-east4-c/instances/${name}"' \
  '      template="https://www.googleapis.com/compute/v1/projects/monad-code/global/instanceTemplates/${base_name}-template-v2"' \
  '      if [[ "${mode}" == "bad-template" && "${index}" -eq 1 ]]; then template="https://www.googleapis.com/compute/v1/projects/other/global/instanceTemplates/escape"; fi' \
  '      instances="$(jq -cn --argjson current "${instances}" --arg name "${name}" --arg id "${id}" --arg instance "${instance}" --arg template "${template}" --arg health "${health}" "\$current + [{name:\$name,id:\$id,instance:\$instance,currentAction:\"NONE\",instanceStatus:\"RUNNING\",version:{instanceTemplate:\$template},instanceHealth:[{detailedHealthState:\$health}]}]")"' \
  '    done' \
  '    printf "%s\n" "${instances}"' \
  '    ;;' \
  '  backend-services)' \
  '    [[ "${3:-}" == "get-health" ]] || exit 2' \
  '    backend="${4:-}"' \
  '    case "${backend}" in' \
  '      e2b-backend-nomad) size=3; base_name=e2b-orch-server ;;' \
  '      e2b-backend-api|e2b-h2c-api) size=2; base_name=e2b-orch-api ;;' \
  '      *) exit 2 ;;' \
  '    esac' \
  '    health_entries="[]"' \
  '    for ((index=1; index<=size; index++)); do' \
  '      state=HEALTHY' \
  '      if [[ "${FAKE_GCLOUD_MODE:-stable}" == "backend-unhealthy" && "${index}" -eq 1 ]]; then state=UNHEALTHY; fi' \
  '      instance="https://www.googleapis.com/compute/v1/projects/monad-code/zones/us-east4-c/instances/${base_name}-${index}"' \
  '      health_entries="$(jq -cn --argjson current "${health_entries}" --arg instance "${instance}" --arg state "${state}" "\$current + [{instance:\$instance,healthState:\$state}]")"' \
  '    done' \
  '    jq -cn --argjson health "${health_entries}" "[{backend:\"fixture\",status:{healthStatus:\$health}}]"' \
  '    ;;' \
  '  ssh)' \
  '    name="${3:?}"' \
  '    if [[ "${FAKE_GCLOUD_MODE:-stable}" == "ssh-fail" ]]; then exit 1; fi' \
  '    case "${name}" in' \
  '      e2b-orch-server-*) base_id=1000 ;;' \
  '      e2b-orch-api-*) base_id=2000 ;;' \
  '      e2b-orch-client-*) base_id=3000 ;;' \
  '      e2b-orch-build-default-*) base_id=4000 ;;' \
  '      *) exit 2 ;;' \
  '    esac' \
  '    expected_id="$((base_id + ${name##*-}))"' \
  '    grep -F "instance/id" <<<"$*" >/dev/null' \
  '    grep -F "attributes/enable-oslogin" <<<"$*" >/dev/null' \
  '    grep -F "${expected_id}" <<<"$*" >/dev/null' \
  '    ;;' \
  '  *) exit 2 ;;' \
  'esac' \
  >"${fake_gcloud}"
chmod 0755 "${fake_gcloud}"

fake_curl="${test_dir}/curl"
# These are literal lines in the generated fixture, not parent-shell expansions.
# shellcheck disable=SC2016
printf '%s\n' \
  '#!/usr/bin/env bash' \
  'set -euo pipefail' \
  'printf "%s\n" "$*" >>"${FAKE_CURL_ARGV_LOG:?}"' \
  'if [[ "$*" == "--fail --silent --show-error --connect-timeout 5 --max-time 10 https://api.example.invalid/health" ]]; then' \
  '  if [[ "${FAKE_CURL_MODE:-healthy}" == "api-unhealthy" ]]; then exit 1; fi' \
  '  printf "%s\n" "ok"' \
  '  exit 0' \
  'fi' \
  '[[ "$*" == "--config -" ]]' \
  'config="$(cat)"' \
  'grep -F "header = \"X-Nomad-Token: fixture-nomad-token\"" <<<"${config}" >/dev/null' \
  'url="$(sed -n "s/^url = \"\\(.*\\)\"$/\\1/p" <<<"${config}")"' \
  'case "${url}" in' \
  '  https://nomad.example.invalid/v1/status/leader)' \
  '    printf "%s\n" "\"10.150.0.11:4647\""' \
  '    ;;' \
  '  https://nomad.example.invalid/v1/status/peers)' \
  '    printf "%s\n" "[\"10.150.0.11:4647\",\"10.150.0.12:4647\",\"10.150.0.13:4647\"]"' \
  '    ;;' \
  '  https://nomad.example.invalid/v1/operator/autopilot/health)' \
  '    if [[ "${FAKE_CURL_MODE:-healthy}" == "nomad-unhealthy" ]]; then' \
  '      printf "%s\n" "{\"Healthy\":false,\"Servers\":[]}"' \
  '    else' \
  '      printf "%s\n" "{\"Healthy\":true,\"Servers\":[{\"Name\":\"e2b-orch-server-1.us-east4\",\"Address\":\"10.150.0.11:4647\",\"Healthy\":true,\"Voter\":true},{\"Name\":\"e2b-orch-server-2.us-east4\",\"Address\":\"10.150.0.12:4647\",\"Healthy\":true,\"Voter\":true},{\"Name\":\"e2b-orch-server-3.us-east4\",\"Address\":\"10.150.0.13:4647\",\"Healthy\":true,\"Voter\":true}]}"' \
  '    fi' \
  '    ;;' \
  '  https://nomad.example.invalid/v1/nodes)' \
  '    if [[ "${FAKE_CURL_MODE:-healthy}" == "client-missing" ]]; then' \
  '      printf "%s\n" "[{\"Name\":\"e2b-orch-client-1\",\"Status\":\"ready\"}]"' \
  '    else' \
  '      printf "%s\n" "[{\"Name\":\"e2b-orch-api-1\",\"Status\":\"ready\"},{\"Name\":\"e2b-orch-api-2\",\"Status\":\"ready\"},{\"Name\":\"e2b-orch-client-1\",\"Status\":\"ready\"},{\"Name\":\"e2b-orch-client-2\",\"Status\":\"ready\"},{\"Name\":\"e2b-orch-build-default-1\",\"Status\":\"ready\"}]"' \
  '    fi' \
  '    ;;' \
  '  *) exit 2 ;;' \
  'esac' \
  >"${fake_curl}"
chmod 0755 "${fake_curl}"

run_stage() {
  local stage="$1"
  local mode="${2:-stable}"
  local max_seconds="${3:-3}"
  local poll_seconds="${4:-0}"
  local curl_mode="${5:-healthy}"

  : >"${test_dir}/gcloud.log"
  : >"${test_dir}/curl-argv.log"
  printf '0\n' >"${test_dir}/counter"
  GCP_PROJECT_ID=monad-code \
    GCP_REGION=us-east4 \
    GCP_ZONE=us-east4-c \
    DOMAIN_NAME=example.invalid \
    PREFIX=e2b- \
    NETWORK_HARDENING_ROLLOUT_STAGE="${stage}" \
    NETWORK_HARDENING_WAIT_SECONDS="${max_seconds}" \
    NETWORK_HARDENING_POLL_SECONDS="${poll_seconds}" \
    GCLOUD_BIN="${fake_gcloud}" \
    CURL_BIN="${fake_curl}" \
    FAKE_GCLOUD_LOG="${test_dir}/gcloud.log" \
    FAKE_GCLOUD_COUNTER="${test_dir}/counter" \
    FAKE_GCLOUD_MODE="${mode}" \
    FAKE_CURL_ARGV_LOG="${test_dir}/curl-argv.log" \
    FAKE_CURL_MODE="${curl_mode}" \
    "${script_dir}/wait-network-hardening-stage.sh"
}

expect_fail() {
  local description="$1"
  shift
  if "$@" >"${test_dir}/stdout" 2>"${test_dir}/stderr"; then
    printf 'expected failure: %s\n' "${description}" >&2
    exit 1
  fi
}

run_stage disabled >/dev/null
[[ ! -s "${test_dir}/gcloud.log" ]]

run_stage network >"${test_dir}/network.output"
grep -F 'Network-hardening stage converged: network.' "${test_dir}/network.output" >/dev/null

run_stage server >"${test_dir}/server.output"
grep -E 'replacements=.*identity_sha256=[0-9a-f]{64}' "${test_dir}/server.output" >/dev/null
grep -F 'backend-services get-health e2b-backend-nomad --global' \
  "${test_dir}/gcloud.log" >/dev/null
test "$(grep -c '^compute ssh e2b-orch-server-' "${test_dir}/gcloud.log")" -eq 3

run_stage api >"${test_dir}/api.output"
grep -E 'replacements=.*identity_sha256=[0-9a-f]{64}' "${test_dir}/api.output" >/dev/null
grep -F 'backend-services get-health e2b-backend-api --global' \
  "${test_dir}/gcloud.log" >/dev/null
grep -F 'backend-services get-health e2b-h2c-api --global' \
  "${test_dir}/gcloud.log" >/dev/null
grep -F 'https://api.example.invalid/health' "${test_dir}/curl-argv.log" >/dev/null
test "$(grep -c '^compute ssh e2b-orch-api-' "${test_dir}/gcloud.log")" -eq 2

run_stage worker >"${test_dir}/worker.output"
grep -E 'replacements=.*identity_sha256=[0-9a-f]{64}' "${test_dir}/worker.output" >/dev/null
test "$(grep -c '^compute ssh e2b-orch-client-' "${test_dir}/gcloud.log")" -eq 2

if grep -F 'fixture-nomad-token' "${test_dir}/curl-argv.log" >/dev/null; then
  printf 'Nomad token leaked into the curl argument vector.\n' >&2
  exit 1
fi

run_stage worker delayed 3 0 >/dev/null
[[ "$(cat "${test_dir}/counter")" -ge 2 ]]
grep -F 'instance-groups managed describe e2b-orch-client-rig --region=us-east4' \
  "${test_dir}/gcloud.log" >/dev/null

run_stage build stable 3 0 >/dev/null
grep -F 'instance-groups managed describe e2b-orch-build-default-rig --region=us-east4' \
  "${test_dir}/gcloud.log" >/dev/null
grep -F 'instance-groups managed describe e2b-orch-loki-ig --zone=us-east4-c' \
  "${test_dir}/gcloud.log" >/dev/null
grep -F 'instance-groups managed describe e2b-clickhouse-ig --zone=us-east4-c' \
  "${test_dir}/gcloud.log" >/dev/null
test "$(grep -c '^compute ssh e2b-orch-build-default-' "${test_dir}/gcloud.log")" -eq 1

expect_fail "unstable MIG times out" run_stage worker unstable 1 1
grep -F 'did not converge' "${test_dir}/stderr" >/dev/null
expect_fail "missing versionTarget.isReached fails closed" run_stage api missing-version 1 1
expect_fail "unhealthy replacement inventory fails closed" \
  run_stage worker inventory-unhealthy 1 1
expect_fail "unexpected replacement template fails closed" \
  run_stage worker bad-template 1 1
expect_fail "load-balancer health failure blocks server convergence" \
  run_stage server backend-unhealthy 1 1
expect_fail "public API failure blocks API convergence" \
  run_stage api stable 1 1 api-unhealthy
expect_fail "Nomad quorum failure blocks server convergence" \
  run_stage server stable 1 1 nomad-unhealthy
expect_fail "missing Nomad client blocks worker convergence" \
  run_stage worker stable 1 1 client-missing
expect_fail "IAP or OS Login failure blocks API convergence" \
  run_stage api ssh-fail 1 1
expect_fail "replacement identity race fails the remote identity proof" \
  run_stage worker identity-race 1 1
expect_fail "replacement identity change invalidates completed evidence" \
  run_stage worker identity-change 1 1

expect_fail "post-replacement evidence requires the deployment domain" \
  env \
    GCP_PROJECT_ID=monad-code \
    GCP_REGION=us-east4 \
    GCP_ZONE=us-east4-c \
    PREFIX=e2b- \
    NETWORK_HARDENING_ROLLOUT_STAGE=server \
    NETWORK_HARDENING_WAIT_SECONDS=1 \
    NETWORK_HARDENING_POLL_SECONDS=0 \
    GCLOUD_BIN="${fake_gcloud}" \
    CURL_BIN="${fake_curl}" \
    FAKE_GCLOUD_LOG="${test_dir}/gcloud.log" \
    FAKE_GCLOUD_COUNTER="${test_dir}/counter" \
    FAKE_GCLOUD_MODE=stable \
    FAKE_CURL_ARGV_LOG="${test_dir}/curl-argv.log" \
    "${script_dir}/wait-network-hardening-stage.sh"
grep -F 'DOMAIN_NAME is required for post-replacement service evidence' \
  "${test_dir}/stderr" >/dev/null

printf 'Network-hardening stage convergence guards passed.\n'
