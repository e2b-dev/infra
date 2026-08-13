#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
wait_script="${script_dir}/wait-orchestrator-release.sh"
test_dir="$(mktemp -d)"
trap 'rm -rf "${test_dir}"' EXIT

expected_source='gcs::https://www.googleapis.com/storage/v1/monad-code-fc-env-pipeline/orchestrator.0123456789ab#2001'
fake_gcloud="${test_dir}/gcloud"
fake_curl="${test_dir}/curl"
curl_argv_log="${test_dir}/curl-argv.log"

cat >"${fake_gcloud}" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
if [[ "$*" == "secrets versions access latest --secret=e2b-nomad-secret-id --project=monad-code" ]]; then
  printf 'fixture-nomad-token\n'
  exit 0
fi
exit 2
EOF
chmod 0700 "${fake_gcloud}"

cat >"${fake_curl}" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >>"${CURL_ARGV_LOG}"
[[ "$#" -eq 2 && "$1" == "--config" ]]
config="$2"
mode="$(stat -c '%a' "${config}" 2>/dev/null || stat -f '%Lp' "${config}")"
(( (8#${mode} & 077) == 0 ))
grep -F 'header = "X-Nomad-Token: fixture-nomad-token"' "${config}" >/dev/null
url="$(sed -n 's/^url = "\(.*\)"$/\1/p' "${config}")"
case "${url}" in
  https://nomad.e2b.monad0.net/v1/job/orchestrator-dev)
    jq -cn --arg source "${EXPECTED_SOURCE}" '{
      ID: "orchestrator-dev",
      Type: "system",
      NodePool: "default",
      Status: "running",
      Version: 4,
      TaskGroups: [{
        Tasks: [{
          Artifacts: [{
            GetterSource: $source,
            RelativeDest: "local/orchestrator"
          }]
        }]
      }]
    }'
    ;;
  'https://nomad.e2b.monad0.net/v1/job/orchestrator-dev/allocations?all=true')
    jq -cn '[
      {
        ID: "alloc-a",
        NodeID: "node-a",
        JobVersion: 4,
        DesiredStatus: "run",
        ClientStatus: "running",
        TaskStates: {start: {State: "running", Failed: false}}
      },
      {
        ID: "alloc-b",
        NodeID: "node-b",
        JobVersion: 4,
        DesiredStatus: "run",
        ClientStatus: "running",
        TaskStates: {start: {State: "running", Failed: false}}
      }
    ]'
    ;;
  https://nomad.e2b.monad0.net/v1/nodes)
    jq -cn '[
      {ID: "node-a", Status: "ready", NodePool: "default"},
      {ID: "node-b", Status: "ready", NodePool: "default"},
      {ID: "api-node", Status: "ready", NodePool: "api"}
    ]'
    ;;
  https://nomad.e2b.monad0.net/v1/service/orchestrator)
    jq -cn '[
      {AllocID: "alloc-a"},
      {AllocID: "alloc-b"}
    ]'
    ;;
  https://nomad.e2b.monad0.net/v1/service/orchestrator-proxy)
    jq -cn '[
      {AllocID: "alloc-a"},
      {AllocID: "alloc-b"}
    ]'
    ;;
  *)
    printf 'unexpected URL: %s\n' "${url}" >&2
    exit 2
    ;;
esac
EOF
chmod 0700 "${fake_curl}"

EXPECTED_SOURCE="${expected_source}" \
CURL_ARGV_LOG="${curl_argv_log}" \
CURL_BIN="${fake_curl}" \
  "${wait_script}" \
    monad-code e2b- e2b.monad0.net "${expected_source}" \
    "${fake_gcloud}" 60 >"${test_dir}/output"

grep -F 'Orchestrator release converged on every ready default-pool client' \
  "${test_dir}/output" >/dev/null
test "$(wc -l <"${curl_argv_log}" | tr -d ' ')" -eq 5
if grep -F 'fixture-nomad-token' "${curl_argv_log}" >/dev/null; then
  printf 'Nomad token leaked into curl argv.\n' >&2
  exit 1
fi

if "${wait_script}" \
  monad-code e2b- e2b.monad0.net 'gcs://unreviewed' \
  "${fake_gcloud}" 60 >"${test_dir}/bad-output" 2>&1; then
  printf 'Invalid orchestrator source unexpectedly passed.\n' >&2
  exit 1
fi
grep -F 'Expected orchestrator artifact source is invalid.' \
  "${test_dir}/bad-output" >/dev/null

printf 'Orchestrator release convergence fixtures passed.\n'
