#!/usr/bin/env bash
set -euo pipefail

project_id="${GCP_PROJECT_ID:?GCP_PROJECT_ID is required}"
region="${GCP_REGION:?GCP_REGION is required}"
zone="${GCP_ZONE:?GCP_ZONE is required}"
prefix="${PREFIX:?PREFIX is required}"
stage="${NETWORK_HARDENING_ROLLOUT_STAGE:?NETWORK_HARDENING_ROLLOUT_STAGE is required}"
domain_name="${DOMAIN_NAME:-}"
gcloud_bin="${GCLOUD_BIN:-gcloud}"
curl_bin="${CURL_BIN:-curl}"
max_seconds="${NETWORK_HARDENING_WAIT_SECONDS:-1800}"
poll_seconds="${NETWORK_HARDENING_POLL_SECONDS:-15}"

case "${stage}" in
  disabled)
    printf 'Network-hardening rollout is disabled; no convergence wait is required.\n'
    exit 0
    ;;
  network | server | api | worker | build) ;;
  *)
    printf 'Unknown network-hardening convergence stage: %s\n' "${stage}" >&2
    exit 2
    ;;
esac

[[ "${zone}" == "${region}-"* ]] || {
  printf 'Network-hardening zone must belong to region %s: %s\n' "${region}" "${zone}" >&2
  exit 2
}
if [[ ! "${max_seconds}" =~ ^[0-9]+$ ]] || ((max_seconds < 1 || max_seconds > 3600)); then
  printf 'Network-hardening wait must be between 1 and 3600 seconds: %s\n' "${max_seconds}" >&2
  exit 2
fi
if [[ ! "${poll_seconds}" =~ ^[0-9]+$ ]] || ((poll_seconds > 60)); then
  printf 'Network-hardening poll interval must be between 0 and 60 seconds: %s\n' "${poll_seconds}" >&2
  exit 2
fi
if [[ ! -x "${gcloud_bin}" ]] && ! command -v "${gcloud_bin}" >/dev/null 2>&1; then
  printf 'Required gcloud command is unavailable: %s\n' "${gcloud_bin}" >&2
  exit 2
fi
command -v jq >/dev/null 2>&1 || {
  printf 'jq is required to verify network-hardening convergence.\n' >&2
  exit 2
}
if [[ "${stage}" != "network" ]]; then
  [[ "${domain_name}" =~ ^[A-Za-z0-9][A-Za-z0-9.-]*[A-Za-z0-9]$ ]] || {
    printf 'DOMAIN_NAME is required for post-replacement service evidence: %s\n' \
      "${domain_name}" >&2
    exit 2
  }
  if [[ ! -x "${curl_bin}" ]] && ! command -v "${curl_bin}" >/dev/null 2>&1; then
    printf 'Required curl command is unavailable: %s\n' "${curl_bin}" >&2
    exit 2
  fi
  command -v shasum >/dev/null 2>&1 || {
    printf 'shasum is required to bind convergence evidence to replacement identities.\n' >&2
    exit 2
  }
fi

iap_firewall="${prefix}orch-iap-remote-connection-firewall-ingress"
public_deny_firewall="${prefix}orch-remote-connection-firewall-ingress"
legacy_allow_firewall="${prefix}orch-internal-remote-connection-firewall-ingress"
nomad_backend="${prefix}backend-nomad"
api_backends=("${prefix}backend-api" "${prefix}h2c-api")
nomad_secret="${prefix}nomad-secret-id"
nomad_host="nomad.${domain_name}"
last_check="starting"
group_instances='[]'
stage_instances='[]'
stage_identity_sha256=''
stage_identity_summary=''
proven_access_sha256=''
nomad_token=''

firewall_ready() {
  local name="$1"
  local mode="$2"
  local document

  last_check="firewall ${name}"
  document="$(
    "${gcloud_bin}" compute firewall-rules describe "${name}" \
      --project="${project_id}" \
      --format=json 2>/dev/null
  )" || return 1

  case "${mode}" in
    iap)
      jq -e '
        .direction == "INGRESS"
        and .disabled != true
        and .priority == 700
        and (.sourceRanges | sort) == ["35.235.240.0/20"]
        and (.targetTags | sort) == ["orch"]
        and ([.allowed[]? | select(.IPProtocol == "tcp") | .ports[]?] | sort)
          == ["22", "3389"]
        and ((.denied // []) | length) == 0
        and .logConfig.enable == true
        and .logConfig.metadata == "EXCLUDE_ALL_METADATA"
      ' <<<"${document}" >/dev/null
      ;;
    public-deny)
      jq -e '
        .direction == "INGRESS"
        and .disabled != true
        and .priority == 800
        and (.sourceRanges | sort) == ["0.0.0.0/0"]
        and (.targetTags | sort) == ["orch"]
        and ([.denied[]? | select(.IPProtocol == "tcp") | .ports[]?] | sort)
          == ["22", "3389"]
        and ((.allowed // []) | length) == 0
        and .logConfig.enable == true
        and .logConfig.metadata == "EXCLUDE_ALL_METADATA"
      ' <<<"${document}" >/dev/null
      ;;
    legacy-shadowed)
      jq -e '
        .direction == "INGRESS"
        and .disabled != true
        and .priority == 900
        and (.sourceRanges | sort) == ["0.0.0.0/0", "35.235.240.0/20"]
        and (.targetTags | sort) == ["orch"]
        and ([.allowed[]? | select(.IPProtocol == "tcp") | .ports[]?] | sort)
          == ["22", "3389"]
        and ((.denied // []) | length) == 0
        and .logConfig.enable == true
        and .logConfig.metadata == "EXCLUDE_ALL_METADATA"
      ' <<<"${document}" >/dev/null
      ;;
  esac
}

managed_group_ready() {
  local name="$1"
  local scope_flag="$2"
  local scope_value="$3"
  local document
  local instances
  local target_size
  local target_templates

  last_check="managed group ${name}"
  document="$(
    "${gcloud_bin}" compute instance-groups managed describe "${name}" \
      "${scope_flag}=${scope_value}" \
      --project="${project_id}" \
      --format=json 2>/dev/null
  )" || return 1

  jq -e '
    (.targetSize | type) == "number"
    and .status.isStable == true
    and .status.versionTarget.isReached == true
    and ((.currentActions.none // 0) == .targetSize)
    and (
      (.currentActions // {})
      | to_entries
      | all(.key == "none" or .value == 0)
    )
  ' <<<"${document}" >/dev/null || return 1

  target_size="$(jq -er '.targetSize' <<<"${document}")" || return 1
  target_templates="$(jq -ce '[.versions[]?.instanceTemplate] | unique' \
    <<<"${document}")" || return 1
  last_check="managed group instance inventory ${name}"
  instances="$(
    "${gcloud_bin}" compute instance-groups managed list-instances "${name}" \
      "${scope_flag}=${scope_value}" \
      --project="${project_id}" \
      --format=json 2>/dev/null
  )" || return 1

  jq -e \
    --arg project_id "${project_id}" \
    --arg region "${region}" \
    --argjson target_size "${target_size}" \
    --argjson target_templates "${target_templates}" '
      type == "array"
      and length == $target_size
      and ($target_size == 0 or ($target_templates | length) > 0)
      and ([.[].id] | unique | length) == length
      and ([.[].name] | unique | length) == length
      and all(.[];
        (.id | type) == "string"
        and (.id | test("^[0-9]+$"))
        and (.name | type) == "string"
        and (.name | test("^[a-z][a-z0-9-]*[a-z0-9]$"))
        and .currentAction == "NONE"
        and .instanceStatus == "RUNNING"
        and (.instance | type) == "string"
        and (
          .instance
          | test(
              "^https://www\\.googleapis\\.com/compute/v1/projects/"
              + $project_id
              + "/zones/"
              + $region
              + "-[a-z]/instances/"
            )
        )
        and (. as $instance | $instance.instance | endswith("/" + $instance.name))
        and (.version.instanceTemplate | type) == "string"
        and (
          . as $instance
          | ($target_templates | index($instance.version.instanceTemplate)) != null
        )
        and (
          .version.instanceTemplate
          | test(
              "^https://www\\.googleapis\\.com/compute/v1/projects/"
              + $project_id
              + "/global/instanceTemplates/"
            )
        )
        and (.instanceHealth | type) == "array"
        and (.instanceHealth | length) > 0
        and all(.instanceHealth[]; .detailedHealthState == "HEALTHY")
      )
    ' <<<"${instances}" >/dev/null || return 1

  group_instances="$(jq -c --arg group "${name}" \
    '[.[] | . + {rolloutGroup: $group}]' <<<"${instances}")" || return 1
}

append_group_instances() {
  stage_instances="$(jq -cn \
    --argjson current "${stage_instances}" \
    --argjson additions "${group_instances}" \
    '$current + $additions')"
}

replacement_identity() {
  local canonical

  canonical="$(jq -cS '
    [
      .[]
      | {
          group: .rolloutGroup,
          id,
          instance,
          template: .version.instanceTemplate
        }
    ]
    | sort_by(.instance)
  ' <<<"${stage_instances}")" || return 1
  stage_identity_sha256="$(printf '%s' "${canonical}" | shasum -a 256 | awk '{print $1}')"
  stage_identity_summary="$(jq -r '
    sort_by(.instance)
    | [
        .[]
        | (.version.instanceTemplate | split("/") | last) as $template
        | "\(.name):\(.id)@\($template)"
      ]
    | join(",")
  ' <<<"${stage_instances}")"
  [[ -n "${stage_identity_sha256}" && -n "${stage_identity_summary}" ]]
}

backend_ready() {
  local backend="$1"
  local exact="$2"
  local instances="$3"
  local document

  last_check="load-balancer backend ${backend}"
  document="$(
    "${gcloud_bin}" compute backend-services get-health "${backend}" \
      --global \
      --project="${project_id}" \
      --format=json 2>/dev/null
  )" || return 1

  jq -e \
    --arg exact "${exact}" \
    --argjson instances "${instances}" '
      (
        if type == "array"
        then [.[] | .status.healthStatus[]?]
        else [(.healthStatus // [])[]]
        end
      ) as $health
      | ($instances | map(.instance) | unique | sort) as $targets
      | ($health | map(.instance) | unique | sort) as $reported
      | ($health | length) > 0
        and all($health[]; .healthState == "HEALTHY")
        and (($targets - $reported) | length) == 0
        and ($exact != "true" or $reported == $targets)
    ' <<<"${document}" >/dev/null
}

load_nomad_token() {
  [[ -n "${nomad_token}" ]] && return 0

  last_check="Nomad ACL token"
  nomad_token="$(
    "${gcloud_bin}" secrets versions access latest \
      --secret="${nomad_secret}" \
      --project="${project_id}" 2>/dev/null
  )" || return 1
  [[ -n "${nomad_token}" ]]
}

nomad_request() {
  local path="$1"

  load_nomad_token || return 1
  {
    printf '%s\n' \
      'fail' \
      'silent' \
      'show-error' \
      'connect-timeout = 5' \
      'max-time = 10'
    printf 'header = "X-Nomad-Token: %s"\n' "${nomad_token}"
    printf 'url = "https://%s%s"\n' "${nomad_host}" "${path}"
  } | "${curl_bin}" --config -
}

nomad_servers_ready() {
  local instances="$1"
  local leader
  local peers
  local autopilot

  backend_ready "${nomad_backend}" true "${instances}" || return 1
  load_nomad_token || return 1

  last_check="Nomad leader"
  leader="$(nomad_request /v1/status/leader 2>/dev/null)" || return 1
  jq -e 'type == "string" and length > 0' <<<"${leader}" >/dev/null || return 1

  last_check="Nomad peers"
  peers="$(nomad_request /v1/status/peers 2>/dev/null)" || return 1
  jq -e --argjson instances "${instances}" '
    type == "array"
    and length == ($instances | length)
    and length == (unique | length)
  ' <<<"${peers}" >/dev/null || return 1
  jq -e --arg leader "$(jq -r . <<<"${leader}")" \
    'index($leader) != null' <<<"${peers}" >/dev/null || return 1

  last_check="Nomad quorum and autopilot"
  autopilot="$(nomad_request /v1/operator/autopilot/health 2>/dev/null)" || return 1
  jq -e \
    --arg region "${region}" \
    --argjson instances "${instances}" \
    --argjson peers "${peers}" '
      ($instances | map(.name) | unique | sort) as $target_names
      | .Healthy == true
        and (.Servers | type) == "array"
        and (.Servers | length) == ($instances | length)
        and all(.Servers[]; .Healthy == true and .Voter == true)
        and (
          [.Servers[].Name | sub("\\." + $region + "$"; "")] | unique | sort
        ) == $target_names
        and ([.Servers[].Address] | unique | sort) == ($peers | unique | sort)
    ' <<<"${autopilot}" >/dev/null
}

nomad_clients_ready() {
  local instances="$1"
  local nodes

  load_nomad_token || return 1
  last_check="Nomad replacement clients"
  nodes="$(nomad_request /v1/nodes 2>/dev/null)" || return 1
  jq -e --argjson instances "${instances}" '
    ($instances | map(.name) | unique) as $target_names
    | . as $nodes
    | type == "array"
      and all($target_names[];
        . as $target
        | ([$nodes[] | select(.Name == $target and .Status == "ready")] | length) == 1
      )
  ' <<<"${nodes}" >/dev/null
}

public_api_ready() {
  last_check="public API load balancer"
  "${curl_bin}" \
    --fail \
    --silent \
    --show-error \
    --connect-timeout 5 \
    --max-time 10 \
    "https://api.${domain_name}/health" \
    >/dev/null 2>&1
}

iap_os_login_ready() {
  local instances="$1"
  local identity_sha256
  local instance_name
  local instance_zone
  local instance_id
  local remote_command
  local access_rows
  local expected_count

  replacement_identity || return 1
  identity_sha256="${stage_identity_sha256}"
  if [[ "${proven_access_sha256}" == "${identity_sha256}" ]]; then
    return 0
  fi

  access_rows="$(jq -er '
    .[]
    | [
        .name,
        (.instance | capture("/zones/(?<zone>[^/]+)/instances/").zone),
        .id
      ]
    | @tsv
  ' <<<"${instances}")" || return 1
  expected_count="$(jq -er 'length' <<<"${instances}")" || return 1
  [[ -n "${access_rows}" && "$(wc -l <<<"${access_rows}" | tr -d ' ')" -eq "${expected_count}" ]] || return 1

  while IFS=$'\t' read -r instance_name instance_zone instance_id; do
    [[ "${instance_name}" =~ ^[a-z][a-z0-9-]*[a-z0-9]$ ]] || return 1
    [[ "${instance_zone}" == "${region}-"* ]] || return 1
    [[ "${instance_id}" =~ ^[0-9]+$ ]] || return 1
    last_check="IAP/OS Login ${instance_name}:${instance_id}"
    remote_command="set -eu; instance_id=\$(curl -fsS --connect-timeout 5 --max-time 10 -H 'Metadata-Flavor: Google' http://metadata.google.internal/computeMetadata/v1/instance/id); os_login=\$(curl -fsS --connect-timeout 5 --max-time 10 -H 'Metadata-Flavor: Google' http://metadata.google.internal/computeMetadata/v1/instance/attributes/enable-oslogin); test \"\$instance_id\" = '${instance_id}'; test \"\$os_login\" = 'TRUE'"
    "${gcloud_bin}" compute ssh "${instance_name}" \
      --project="${project_id}" \
      --zone="${instance_zone}" \
      --tunnel-through-iap \
      --quiet \
      --strict-host-key-checking=no \
      --ssh-flag="-o ConnectTimeout=10" \
      --ssh-flag="-o ConnectionAttempts=1" \
      --command="${remote_command}" \
      >/dev/null 2>&1 || return 1
  done <<<"${access_rows}"

  proven_access_sha256="${identity_sha256}"
}

collect_stage_instances() {
  stage_instances='[]'
  case "${stage}" in
    server)
      managed_group_ready "${prefix}orch-server-rig" --region "${region}" || return 1
      append_group_instances || return 1
      ;;
    api)
      managed_group_ready "${prefix}orch-api-ig" --zone "${zone}" || return 1
      append_group_instances || return 1
      ;;
    worker)
      managed_group_ready "${prefix}orch-client-rig" --region "${region}" || return 1
      append_group_instances || return 1
      ;;
    build)
      managed_group_ready "${prefix}orch-build-default-rig" --region "${region}" || return 1
      append_group_instances || return 1
      managed_group_ready "${prefix}orch-loki-ig" --zone "${zone}" || return 1
      append_group_instances || return 1
      managed_group_ready "${prefix}clickhouse-ig" --zone "${zone}" || return 1
      append_group_instances || return 1
      ;;
  esac
}

stage_ready() {
  local evidence_identity_sha256

  firewall_ready "${iap_firewall}" iap || return 1
  firewall_ready "${public_deny_firewall}" public-deny || return 1
  firewall_ready "${legacy_allow_firewall}" legacy-shadowed || return 1

  if [[ "${stage}" == "network" ]]; then
    return 0
  fi

  collect_stage_instances || return 1
  replacement_identity || return 1
  evidence_identity_sha256="${stage_identity_sha256}"

  case "${stage}" in
    server)
      nomad_servers_ready "${stage_instances}" || return 1
      ;;
    api)
      for backend in "${api_backends[@]}"; do
        backend_ready "${backend}" false "${stage_instances}" || return 1
      done
      public_api_ready || return 1
      nomad_clients_ready "${stage_instances}" || return 1
      ;;
    worker | build)
      nomad_clients_ready "${stage_instances}" || return 1
      ;;
  esac
  iap_os_login_ready "${stage_instances}" || return 1

  # Re-read the stable MIG inventory after all remote checks. If a replacement
  # changed while evidence was being gathered, discard it and retry every
  # identity-bound proof against the new instances.
  collect_stage_instances || return 1
  replacement_identity || return 1
  if [[ "${stage_identity_sha256}" != "${evidence_identity_sha256}" ]]; then
    last_check="replacement identity changed during post-replacement evidence"
    return 1
  fi
}

start_time="$(date +%s)"
deadline=$((start_time + max_seconds))

while true; do
  if stage_ready; then
    if [[ "${stage}" == "network" ]]; then
      printf 'Network-hardening stage converged: %s.\n' "${stage}"
    else
      printf 'Network-hardening stage converged: %s replacements=%s identity_sha256=%s.\n' \
        "${stage}" "${stage_identity_summary}" "${stage_identity_sha256}"
    fi
    exit 0
  fi

  now="$(date +%s)"
  if ((now >= deadline)); then
    printf 'Network-hardening stage %s did not converge within %s seconds at %s.\n' \
      "${stage}" "${max_seconds}" "${last_check}" >&2
    exit 1
  fi

  printf 'Waiting for network-hardening stage %s at %s (%ss elapsed).\n' \
    "${stage}" "${last_check}" "$((now - start_time))" >&2
  sleep "${poll_seconds}"
done
