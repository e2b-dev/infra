#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
wait_script="${script_dir}/wait-workload-cluster.sh"
test_dir="$(mktemp -d)"
trap 'rm -rf "${test_dir}"' EXIT

fake_gcloud="${test_dir}/gcloud"
gcloud_log="${test_dir}/gcloud.log"
cat >"${fake_gcloud}" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

printf '%s\n' "$*" >>"${FAKE_GCLOUD_LOG}"

if [[ "${1:-}" == "compute" \
  && "${2:-}" == "instance-groups" \
  && "${3:-}" == "managed" \
  && "${4:-}" == "describe" ]]; then
  case "${5:-}" in
    e2b-orch-server-rig) target_size=3 ;;
    e2b-orch-build-default-rig) target_size=1 ;;
    e2b-orch-client-rig) target_size=1 ;;
    e2b-orch-api-ig) target_size=1 ;;
    *)
      printf 'unexpected instance group: %s\n' "${5:-}" >&2
      exit 2
      ;;
  esac
  jq -cn --argjson target_size "${target_size}" '
    {
      targetSize: $target_size,
      status: {
        isStable: true,
        versionTarget: {isReached: true}
      },
      currentActions: {
        none: $target_size,
        creating: 0,
        deleting: 0,
        recreating: 0,
        restarting: 0
      }
    }
  '
  exit 0
fi

if [[ "${1:-}" == "compute" \
  && "${2:-}" == "backend-services" \
  && "${3:-}" == "get-health" ]]; then
  jq -cn '
    [{
      backend: "fixture",
      status: {
        healthStatus: [
          {instance: "server-1", healthState: "HEALTHY"},
          {instance: "server-2", healthState: "HEALTHY"},
          {instance: "server-3", healthState: "HEALTHY"}
        ]
      }
    }]
  '
  exit 0
fi

if [[ "${1:-}" == "certificate-manager" \
  && "${2:-}" == "certificates" \
  && "${3:-}" == "describe" ]]; then
  printf 'ACTIVE\n'
  exit 0
fi

if [[ "${1:-}" == "compute" \
  && "${2:-}" == "forwarding-rules" \
  && "${3:-}" == "describe" ]]; then
  printf '203.0.113.24\n'
  exit 0
fi

if [[ "${1:-}" == "secrets" \
  && "${2:-}" == "versions" \
  && "${3:-}" == "access" ]]; then
  printf 'fixture-nomad-token\n'
  exit 0
fi

printf 'unexpected fake gcloud command: %s\n' "$*" >&2
exit 2
EOF
chmod 0755 "${fake_gcloud}"

fake_dig="${test_dir}/dig"
cat >"${fake_dig}" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
[[ "$*" == "+short A nomad.example.invalid" ]]
printf '203.0.113.24\n'
EOF
chmod 0755 "${fake_dig}"

fake_curl="${test_dir}/curl"
curl_argv_log="${test_dir}/curl-argv.log"
cat >"${fake_curl}" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

printf '%s\n' "$*" >>"${FAKE_CURL_ARGV_LOG}"
[[ "$*" == "--config -" ]]
config="$(cat)"
grep -F 'header = "X-Nomad-Token: fixture-nomad-token"' \
  <<<"${config}" >/dev/null
url="$(
  sed -n 's/^url = "\(.*\)"$/\1/p' <<<"${config}"
)"
case "${url}" in
  https://nomad.example.invalid/v1/status/leader)
    printf '"10.0.0.1:4647"\n'
    ;;
  https://nomad.example.invalid/v1/status/peers)
    printf '["10.0.0.1:4647","10.0.0.2:4647","10.0.0.3:4647"]\n'
    ;;
  https://nomad.example.invalid/v1/operator/autopilot/health)
    printf '{"Healthy":true,"Servers":[{},{},{}]}\n'
    ;;
  https://nomad.example.invalid/v1/nodes)
    printf '[{"Status":"ready"},{"Status":"ready"},{"Status":"ready"}]\n'
    ;;
  *)
    printf 'unexpected Nomad URL: %s\n' "${url}" >&2
    exit 2
    ;;
esac
EOF
chmod 0755 "${fake_curl}"

export FAKE_GCLOUD_LOG="${gcloud_log}"
export FAKE_CURL_ARGV_LOG="${curl_argv_log}"

CURL_BIN="${fake_curl}" \
DIG_BIN="${fake_dig}" \
  "${wait_script}" \
  monad-code us-east4 us-east4-a e2b- example.invalid \
  "${fake_gcloud}" 60 >"${test_dir}/ready.output"

grep -F \
  'One-workcell cluster bootstrap is ready: servers=3 clients>=3' \
  "${test_dir}/ready.output" >/dev/null
test "$(grep -c '^compute instance-groups managed describe ' "${gcloud_log}")" -eq 4
grep -F \
  'compute backend-services get-health e2b-backend-nomad --global' \
  "${gcloud_log}" >/dev/null
grep -F \
  'secrets versions access latest --secret=e2b-nomad-secret-id' \
  "${gcloud_log}" >/dev/null
if grep -F 'fixture-nomad-token' "${curl_argv_log}" >/dev/null; then
  printf 'Nomad token leaked into the curl argument vector.\n' >&2
  exit 1
fi

if CURL_BIN="${fake_curl}" DIG_BIN="${fake_dig}" \
  "${wait_script}" \
  monad-code us-east4 us-east4-a e2b- example.invalid \
  "${fake_gcloud}" 59 >"${test_dir}/short.output" 2>&1; then
  printf 'Expected a sub-minute cluster wait bound to be rejected.\n' >&2
  exit 1
fi
grep -F 'Cluster wait must be between 60 and 3600 seconds' \
  "${test_dir}/short.output" >/dev/null

if CURL_BIN="${fake_curl}" DIG_BIN="${fake_dig}" \
  "${wait_script}" \
  monad-code us-east4 us-east4-a e2b- example.invalid \
  "${fake_gcloud}" 3601 >"${test_dir}/long.output" 2>&1; then
  printf 'Expected an over-hour cluster wait bound to be rejected.\n' >&2
  exit 1
fi
grep -F 'Cluster wait must be between 60 and 3600 seconds' \
  "${test_dir}/long.output" >/dev/null

if CURL_BIN="${fake_curl}" DIG_BIN="${fake_dig}" \
  "${wait_script}" \
  monad-code us-east4 us-west1-a e2b- example.invalid \
  "${fake_gcloud}" 60 >"${test_dir}/zone.output" 2>&1; then
  printf 'Expected a cross-region cluster wait zone to be rejected.\n' >&2
  exit 1
fi
grep -F 'Cluster wait zone must belong to region us-east4' \
  "${test_dir}/zone.output" >/dev/null

printf 'Workload cluster readiness wait fixtures passed without contacting GCP.\n'
