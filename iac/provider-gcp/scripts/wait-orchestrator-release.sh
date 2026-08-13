#!/usr/bin/env bash
set -euo pipefail

project_id="${1:?usage: wait-orchestrator-release.sh PROJECT PREFIX DOMAIN EXPECTED_SOURCE [GCLOUD_BIN] [MAX_SECONDS]}"
prefix="${2:?usage: wait-orchestrator-release.sh PROJECT PREFIX DOMAIN EXPECTED_SOURCE [GCLOUD_BIN] [MAX_SECONDS]}"
domain="${3:?usage: wait-orchestrator-release.sh PROJECT PREFIX DOMAIN EXPECTED_SOURCE [GCLOUD_BIN] [MAX_SECONDS]}"
expected_source="${4:?usage: wait-orchestrator-release.sh PROJECT PREFIX DOMAIN EXPECTED_SOURCE [GCLOUD_BIN] [MAX_SECONDS]}"
gcloud_bin="${5:-gcloud}"
max_seconds="${6:-600}"
curl_bin="${CURL_BIN:-curl}"

[[ "${max_seconds}" =~ ^[0-9]+$ ]] && (( max_seconds >= 60 && max_seconds <= 1800 )) || {
  printf 'Orchestrator release wait must be between 60 and 1800 seconds: %s\n' \
    "${max_seconds}" >&2
  exit 2
}
[[ "${expected_source}" =~ ^gcs::https://www.googleapis.com/storage/v1/[a-z0-9][a-z0-9._-]+/orchestrator\.[0-9a-f]{12,40}#[1-9][0-9]*$ ]] || {
  printf 'Expected orchestrator artifact source is invalid.\n' >&2
  exit 2
}

for command_path in "${gcloud_bin}" "${curl_bin}" jq; do
  if [[ ! -x "${command_path}" ]] && ! command -v "${command_path}" >/dev/null 2>&1; then
    printf 'Required orchestrator release command is unavailable: %s\n' \
      "${command_path}" >&2
    exit 2
  fi
done

nomad_secret="${prefix}nomad-secret-id"
nomad_host="nomad.${domain}"
nomad_token="$("${gcloud_bin}" secrets versions access latest \
  --secret="${nomad_secret}" --project="${project_id}" 2>/dev/null)"
[[ -n "${nomad_token}" ]] || {
  printf 'Could not obtain the Nomad release-read token.\n' >&2
  exit 1
}

temp_dir="$(mktemp -d "${TMPDIR:-/tmp}/orchestrator-release-wait.XXXXXX")"
chmod 0700 "${temp_dir}"
curl_config="${temp_dir}/curl.conf"
cleanup() {
  nomad_token=""
  rm -rf -- "${temp_dir}"
}
trap cleanup EXIT HUP INT TERM
umask 077

nomad_request() {
  local path="$1"
  {
    printf '%s\n' \
      'fail' \
      'silent' \
      'show-error' \
      'max-time = 10'
    printf 'header = "X-Nomad-Token: %s"\n' "${nomad_token}"
    printf 'url = "https://%s%s"\n' "${nomad_host}" "${path}"
  } >"${curl_config}"
  chmod 0600 "${curl_config}"
  "${curl_bin}" --config "${curl_config}"
}

release_converged() {
  local job
  local allocations
  local nodes
  local orchestrator_services
  local proxy_services

  job="$(nomad_request /v1/job/orchestrator-dev 2>/dev/null)" || return 1
  allocations="$(
    nomad_request '/v1/job/orchestrator-dev/allocations?all=true' 2>/dev/null
  )" || return 1
  nodes="$(nomad_request /v1/nodes 2>/dev/null)" || return 1
  orchestrator_services="$(
    nomad_request /v1/service/orchestrator 2>/dev/null
  )" || return 1
  proxy_services="$(
    nomad_request /v1/service/orchestrator-proxy 2>/dev/null
  )" || return 1

  jq -ne \
    --arg expected_source "${expected_source}" \
    --argjson job "${job}" \
    --argjson allocations "${allocations}" \
    --argjson nodes "${nodes}" \
    --argjson orchestrator_services "${orchestrator_services}" \
    --argjson proxy_services "${proxy_services}" '
      [
        $nodes[]
        | select(.Status == "ready" and .NodePool == "default")
        | .ID
      ] | sort as $ready_nodes
      | [
          $allocations[]
          | select(.JobVersion == $job.Version)
          | select(
              .DesiredStatus == "run"
              and .ClientStatus == "running"
              and .TaskStates.start.State == "running"
              and .TaskStates.start.Failed == false
            )
        ] as $current_allocations
      | [$current_allocations[].NodeID] | sort as $allocation_nodes
      | [$current_allocations[].ID] | sort as $allocation_ids
      | [$orchestrator_services[].AllocID] | sort as $orchestrator_ids
      | [$proxy_services[].AllocID] | sort as $proxy_ids
      | $job.ID == "orchestrator-dev"
        and $job.Type == "system"
        and $job.NodePool == "default"
        and $job.Status == "running"
        and ($job.Version | type) == "number"
        and ($ready_nodes | length) > 0
        and $allocation_nodes == $ready_nodes
        and ($allocation_ids | unique | length) == ($allocation_ids | length)
        and $orchestrator_ids == $allocation_ids
        and $proxy_ids == $allocation_ids
        and (
          [
            $job.TaskGroups[]?.Tasks[]?.Artifacts[]?
            | select(.RelativeDest == "local/orchestrator")
            | .GetterSource
          ]
          == [$expected_source]
        )
    ' >/dev/null
}

start_time="$(date +%s)"
deadline=$((start_time + max_seconds))
while true; do
  if release_converged; then
    printf 'Orchestrator release converged on every ready default-pool client with exact source %s.\n' \
      "${expected_source}"
    exit 0
  fi

  now="$(date +%s)"
  if (( now >= deadline )); then
    printf 'Orchestrator release convergence timed out after %s seconds.\n' \
      "${max_seconds}" >&2
    exit 1
  fi
  printf 'Waiting for exact orchestrator system-job convergence (%ss elapsed).\n' \
    "$((now - start_time))" >&2
  remaining=$((deadline - now))
  sleep_for=10
  if (( remaining < sleep_for )); then
    sleep_for="${remaining}"
  fi
  sleep "${sleep_for}"
done
