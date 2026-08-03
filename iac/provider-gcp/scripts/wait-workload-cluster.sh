#!/usr/bin/env bash
set -euo pipefail

project_id="${1:?usage: wait-workload-cluster.sh PROJECT REGION ZONE PREFIX DOMAIN [GCLOUD_BIN] [MAX_SECONDS]}"
region="${2:?usage: wait-workload-cluster.sh PROJECT REGION ZONE PREFIX DOMAIN [GCLOUD_BIN] [MAX_SECONDS]}"
zone="${3:?usage: wait-workload-cluster.sh PROJECT REGION ZONE PREFIX DOMAIN [GCLOUD_BIN] [MAX_SECONDS]}"
prefix="${4:?usage: wait-workload-cluster.sh PROJECT REGION ZONE PREFIX DOMAIN [GCLOUD_BIN] [MAX_SECONDS]}"
domain="${5:?usage: wait-workload-cluster.sh PROJECT REGION ZONE PREFIX DOMAIN [GCLOUD_BIN] [MAX_SECONDS]}"
gcloud_bin="${6:-gcloud}"
max_seconds="${7:-1800}"
curl_bin="${CURL_BIN:-curl}"
dig_bin="${DIG_BIN:-dig}"

[[ "${max_seconds}" =~ ^[0-9]+$ ]] && (( max_seconds >= 60 && max_seconds <= 3600 )) || {
  printf 'Cluster wait must be between 60 and 3600 seconds: %s\n' "${max_seconds}" >&2
  exit 2
}
[[ "${zone}" == "${region}-"* ]] || {
  printf 'Cluster wait zone must belong to region %s: %s\n' "${region}" "${zone}" >&2
  exit 2
}

for command_path in "${gcloud_bin}" "${curl_bin}" "${dig_bin}" jq; do
  if [[ ! -x "${command_path}" ]] && ! command -v "${command_path}" >/dev/null 2>&1; then
    printf 'Required cluster wait command is unavailable: %s\n' "${command_path}" >&2
    exit 2
  fi
done

server_group="${prefix}orch-server-rig"
build_group="${prefix}orch-build-default-rig"
client_group="${prefix}orch-client-rig"
api_group="${prefix}orch-api-ig"
nomad_backend="${prefix}backend-nomad"
certificate="${prefix}root-cert"
forwarding_rule="${prefix}forwarding-rule-https"
nomad_secret="${prefix}nomad-secret-id"
nomad_host="nomad.${domain}"

last_stage="starting"
nomad_token=""

managed_group_ready() {
  local name="$1"
  local scope_flag="$2"
  local scope_value="$3"
  local expected_size="$4"
  local document

  document="$(
    "${gcloud_bin}" compute instance-groups managed describe "${name}" \
      "${scope_flag}=${scope_value}" \
      --project="${project_id}" \
      --format=json 2>/dev/null
  )" || return 1

  jq -e \
    --argjson expected_size "${expected_size}" '
      .targetSize == $expected_size
      and .status.isStable == true
      and .status.versionTarget.isReached == true
      and ((.currentActions.none // 0) == $expected_size)
      and (
        (.currentActions // {})
        | to_entries
        | all(.key == "none" or .value == 0)
      )
    ' <<<"${document}" >/dev/null
}

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
  } | "${curl_bin}" --config -
}

check_cluster() {
  local backend_health
  local certificate_state
  local forwarding_ip
  local dns_addresses
  local leader
  local peers
  local autopilot
  local nodes

  last_stage="server MIG"
  managed_group_ready "${server_group}" --region "${region}" 3 || return 1
  last_stage="build MIG"
  managed_group_ready "${build_group}" --region "${region}" 1 || return 1
  last_stage="client MIG"
  managed_group_ready "${client_group}" --region "${region}" 2 || return 1
  last_stage="API MIG"
  managed_group_ready "${api_group}" --zone "${zone}" 2 || return 1

  last_stage="Nomad load-balancer health"
  backend_health="$(
    "${gcloud_bin}" compute backend-services get-health "${nomad_backend}" \
      --global \
      --project="${project_id}" \
      --format=json 2>/dev/null
  )" || return 1
  jq -e '
    (
      if type == "array"
      then [.[] | .status.healthStatus[]?]
      else [(.healthStatus // [])[]]
      end
    ) as $health
    | ($health | length) == 3
      and all($health[]; .healthState == "HEALTHY")
  ' <<<"${backend_health}" >/dev/null || return 1

  last_stage="managed TLS certificate"
  certificate_state="$(
    "${gcloud_bin}" certificate-manager certificates describe "${certificate}" \
      --location=global \
      --project="${project_id}" \
      --format='value(managed.state)' 2>/dev/null
  )" || return 1
  [[ "${certificate_state}" == "ACTIVE" ]] || return 1

  last_stage="Nomad forwarding rule"
  forwarding_ip="$(
    "${gcloud_bin}" compute forwarding-rules describe "${forwarding_rule}" \
      --global \
      --project="${project_id}" \
      --format='value(IPAddress)' 2>/dev/null
  )" || return 1
  [[ "${forwarding_ip}" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]] || return 1

  last_stage="Nomad DNS"
  dns_addresses="$("${dig_bin}" +short A "${nomad_host}" 2>/dev/null)" || return 1
  grep -Fqx "${forwarding_ip}" <<<"${dns_addresses}" || return 1

  if [[ -z "${nomad_token}" ]]; then
    last_stage="Nomad ACL token"
    nomad_token="$(
      "${gcloud_bin}" secrets versions access latest \
        --secret="${nomad_secret}" \
        --project="${project_id}" 2>/dev/null
    )" || return 1
    [[ -n "${nomad_token}" ]] || return 1
  fi

  last_stage="Nomad leader"
  leader="$(nomad_request /v1/status/leader 2>/dev/null)" || return 1
  jq -e 'type == "string" and length > 0' <<<"${leader}" >/dev/null || return 1

  last_stage="Nomad peers"
  peers="$(nomad_request /v1/status/peers 2>/dev/null)" || return 1
  jq -e 'type == "array" and length == 3' <<<"${peers}" >/dev/null || return 1

  last_stage="Nomad autopilot"
  autopilot="$(nomad_request /v1/operator/autopilot/health 2>/dev/null)" || return 1
  jq -e '.Healthy == true and (.Servers | type == "array" and length == 3)' \
    <<<"${autopilot}" >/dev/null || return 1

  last_stage="Nomad clients"
  nodes="$(nomad_request /v1/nodes 2>/dev/null)" || return 1
  jq -e '
    type == "array"
    and ([.[] | select(.Status == "ready")] | length) >= 3
  ' <<<"${nodes}" >/dev/null || return 1
}

start_time="$(date +%s)"
deadline=$((start_time + max_seconds))

while true; do
  if check_cluster; then
    printf 'Invited-beta cluster bootstrap is ready: servers=3 clients>=3 endpoint=https://%s.\n' \
      "${nomad_host}"
    exit 0
  fi

  now="$(date +%s)"
  if (( now >= deadline )); then
    printf 'Cluster bootstrap readiness timed out after %s seconds at stage: %s.\n' \
      "${max_seconds}" "${last_stage}" >&2
    exit 1
  fi

  printf 'Waiting for cluster bootstrap at stage: %s (%ss elapsed).\n' \
    "${last_stage}" "$((now - start_time))" >&2
  remaining=$((deadline - now))
  sleep_for=15
  if (( remaining < sleep_for )); then
    sleep_for="${remaining}"
  fi
  sleep "${sleep_for}"
done
