#!/usr/bin/env bash
set -euo pipefail

stage="${1:?usage: assert-network-hardening-checkpoint.sh STAGE CHECKPOINT PROJECT REGION ZONE PREFIX REPO_ROOT}"
checkpoint="${2:?usage: assert-network-hardening-checkpoint.sh STAGE CHECKPOINT PROJECT REGION ZONE PREFIX REPO_ROOT}"
project_id="${3:?usage: assert-network-hardening-checkpoint.sh STAGE CHECKPOINT PROJECT REGION ZONE PREFIX REPO_ROOT}"
region="${4:?usage: assert-network-hardening-checkpoint.sh STAGE CHECKPOINT PROJECT REGION ZONE PREFIX REPO_ROOT}"
zone="${5:?usage: assert-network-hardening-checkpoint.sh STAGE CHECKPOINT PROJECT REGION ZONE PREFIX REPO_ROOT}"
prefix="${6:?usage: assert-network-hardening-checkpoint.sh STAGE CHECKPOINT PROJECT REGION ZONE PREFIX REPO_ROOT}"
repo_root="${7:?usage: assert-network-hardening-checkpoint.sh STAGE CHECKPOINT PROJECT REGION ZONE PREFIX REPO_ROOT}"

case "${stage}" in
  network)
    expected_checks='["control_plane_healthy","iap_tunnel_access","os_login_admin_access"]'
    ;;
  server)
    expected_checks='["api_load_balancer_healthy","iap_tunnel_access","nomad_quorum_healthy","os_login_admin_access","target_pool_healthy"]'
    ;;
  api)
    expected_checks='["api_load_balancer_healthy","iap_tunnel_access","nomad_quorum_healthy","os_login_admin_access","target_pool_healthy"]'
    ;;
  worker)
    expected_checks='["durable_sessions_preserved","iap_tunnel_access","os_login_admin_access","target_pool_drained","zero_target_allocations","zero_target_workcells"]'
    ;;
  build)
    expected_checks='["build_queue_quiesced","iap_tunnel_access","os_login_admin_access","target_pool_drained","zero_target_allocations","zero_target_workcells"]'
    ;;
  *)
    printf 'Unknown network-hardening rollout stage: %s\n' "${stage}" >&2
    exit 2
    ;;
esac

[[ -f "${checkpoint}" && ! -L "${checkpoint}" ]] || {
  printf 'Network-hardening checkpoint must be a regular, non-symlink file: %s\n' "${checkpoint}" >&2
  exit 1
}
mode_bits="$(stat -c '%a' "${checkpoint}" 2>/dev/null || stat -f '%Lp' "${checkpoint}")"
if (( (8#${mode_bits} & 077) != 0 )); then
  printf 'Network-hardening checkpoint must be private (mode 0600): %s (mode %s)\n' \
    "${checkpoint}" "${mode_bits}" >&2
  exit 1
fi

git_head="$(git -C "${repo_root}" rev-parse --verify HEAD)"
now="$(date -u +%s)"
jq -eS \
  --arg stage "${stage}" \
  --arg project_id "${project_id}" \
  --arg region "${region}" \
  --arg zone "${zone}" \
  --arg prefix "${prefix}" \
  --arg git_head "${git_head}" \
  --argjson now "${now}" \
  --argjson expected_checks "${expected_checks}" '
    (keys | sort) == [
      "checks",
      "evidence",
      "expires_unix",
      "gcp_project_id",
      "gcp_region",
      "gcp_zone",
      "observed_unix",
      "operator_principal",
      "prefix",
      "schema_version",
      "source_git_head",
      "stage"
    ]
    and .schema_version == 1
    and .stage == $stage
    and .gcp_project_id == $project_id
    and .gcp_region == $region
    and .gcp_zone == $zone
    and .prefix == $prefix
    and .source_git_head == $git_head
    and (.operator_principal | type) == "string"
    and (.operator_principal | test("^[^[:space:]@]+@[^[:space:]@]+$"))
    and (.observed_unix | type) == "number"
    and (.expires_unix | type) == "number"
    and (.observed_unix | floor) == .observed_unix
    and (.expires_unix | floor) == .expires_unix
    and .observed_unix <= $now
    and .observed_unix >= ($now - 3600)
    and .expires_unix >= $now
    and .expires_unix <= (.observed_unix + 3600)
    and (.checks | keys | sort) == $expected_checks
    and all(.checks[]; . == true)
    and (.evidence | keys | sort) == $expected_checks
    and all(.evidence[]; type == "string" and length > 0)
  ' "${checkpoint}" >/dev/null || {
  printf 'Network-hardening checkpoint is stale, mismatched, incomplete, or malformed: %s\n' \
    "${checkpoint}" >&2
  exit 1
}

printf 'Network-hardening %s checkpoint verified: %s\n' "${stage}" "${checkpoint}"
