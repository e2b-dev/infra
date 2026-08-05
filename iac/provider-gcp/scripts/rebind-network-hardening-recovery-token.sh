#!/usr/bin/env bash
set -euo pipefail

old_token="${1:?usage: rebind-network-hardening-recovery-token.sh OLD_TOKEN NEW_TOKEN STATE_BUCKET PROJECT REGION STAGE ENVIRONMENT STATE_PREFIX REPO_ROOT GCLOUD_BIN}"
new_token="${2:?replacement token path is required}"
state_bucket="${3:?state bucket is required}"
project="${4:?project is required}"
region="${5:?region is required}"
stage="${6:?stage is required}"
environment="${7:?environment is required}"
state_prefix="${8:?state prefix is required}"
repo_root="${9:?repository root is required}"
gcloud_bin="${10:?gcloud binary is required}"
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
lease_script="${script_dir}/rollout-mutation-lease.sh"
recovery_assertion="${script_dir}/assert-network-hardening-recovery-token.sh"

[[ "${stage}" == "network" ]] || {
  printf 'Recovery source rebinding is restricted to the network stage.\n' >&2
  exit 1
}
[[ "${old_token}" != "${new_token}" ]] || {
  printf 'Replacement recovery token must use a distinct path.\n' >&2
  exit 1
}
[[ ! -e "${new_token}" && ! -L "${new_token}" ]] || {
  printf 'Replacement recovery token path already exists: %s\n' "${new_token}" >&2
  exit 1
}

source_head="$(git -C "${repo_root}" rev-parse --verify HEAD)"
[[ "${source_head}" =~ ^[0-9a-f]{40}$ ]] || {
  printf 'Current recovery source is not an exact Git commit.\n' >&2
  exit 1
}
if [[ -n "$(git -C "${repo_root}" status --porcelain --untracked-files=no)" ]]; then
  printf 'Recovery source rebinding requires a clean tracked worktree.\n' >&2
  exit 1
fi

"${lease_script}" assert-held \
  "${gcloud_bin}" "${state_bucket}" "${project}" "${region}" \
  "${old_token}" >/dev/null

old_holder="$(jq -er '.holder' "${old_token}")"
old_generation="$(jq -er '.generation | tostring' "${old_token}")"
holder_prefix="cluster-apply:${stage}:${environment}:${state_prefix}:"
[[ "${old_holder}" == "${holder_prefix}"* ]] || {
  printf 'Original lease holder does not match this exact stage, environment, and backend.\n' >&2
  exit 1
}
holder_suffix="${old_holder#"${holder_prefix}"}"
old_source_head="${holder_suffix%%:*}"
old_holder_digest="${holder_suffix#*:}"
[[ "${old_source_head}" =~ ^[0-9a-f]{40}$ \
  && "${old_holder_digest}" =~ ^[0-9a-f]{64}$ \
  && "${holder_suffix}" == "${old_source_head}:${old_holder_digest}" ]] || {
  printf 'Original lease holder does not carry one exact source head and digest.\n' >&2
  exit 1
}
[[ "${old_source_head}" != "${source_head}" ]] || {
  printf 'Recovery lease already belongs to the current source head.\n' >&2
  exit 1
}
git -C "${repo_root}" cat-file -e "${old_source_head}^{commit}" 2>/dev/null || {
  printf 'Original recovery source commit is not present locally: %s\n' \
    "${old_source_head}" >&2
  exit 1
}
git -C "${repo_root}" merge-base --is-ancestor \
  "${old_source_head}" "${source_head}" || {
  printf 'Replacement recovery source must descend from the original source.\n' >&2
  exit 1
}

# This incident-only handoff is intentionally narrower than a generic
# descendant check. It can carry the held network-stage lease only across the
# reviewed firewall-precedence repair and its guards/runbook—not unrelated
# fleet, image, provider, or workload changes.
while IFS= read -r changed_path; do
  case "${changed_path}" in
    docs/ARCHITECTURE.md|\
    docs/MONAD_GCP_FOUNDATION.md|\
    docs/MONAD_GCP_NETWORK_HARDENING.md|\
    iac/provider-gcp/Makefile|\
    iac/provider-gcp/nomad-cluster/network/main.tf|\
    iac/provider-gcp/scripts/assert-network-hardening-stage-plan.sh|\
    iac/provider-gcp/scripts/assert-network-hardening-recovery-token.sh|\
    iac/provider-gcp/scripts/rebind-network-hardening-recovery-token.sh|\
    iac/provider-gcp/scripts/rollout-mutation-lease.sh|\
    iac/provider-gcp/scripts/test-network-hardening-rollout.sh|\
    iac/provider-gcp/scripts/test-network-hardening-stage-wait.sh|\
    iac/provider-gcp/scripts/test-network-security-guards.sh|\
    iac/provider-gcp/scripts/test-rebind-network-hardening-recovery-token.sh|\
    iac/provider-gcp/scripts/test-rollout-mutation-lease.sh|\
    iac/provider-gcp/scripts/test-workload-release.sh|\
    iac/provider-gcp/scripts/wait-network-hardening-stage.sh)
      ;;
    *)
      printf 'Recovery source diff escapes the reviewed network boundary: %s\n' \
        "${changed_path}" >&2
      exit 1
      ;;
  esac
done < <(git -C "${repo_root}" diff --name-only "${old_source_head}..${source_head}")

[[ "$(git -C "${repo_root}" rev-parse --verify HEAD)" == "${source_head}" ]] || {
  printf 'Recovery source head changed during descendant verification.\n' >&2
  exit 1
}
if [[ -n "$(git -C "${repo_root}" status --porcelain --untracked-files=no)" ]]; then
  printf 'Recovery source became dirty during descendant verification.\n' >&2
  exit 1
fi

holder_digest="$(
  {
    printf '%s\n' "${old_holder}" "${old_generation}" "${source_head}"
    date -u +%s
  } | shasum -a 256 | awk '{print $1}'
)"
new_holder="${holder_prefix}${source_head}:${holder_digest}"

"${lease_script}" transfer \
  "${gcloud_bin}" "${old_token}" "${new_holder}" "${new_token}"
"${recovery_assertion}" \
  "${new_token}" "${state_bucket}" "${project}" "${region}" \
  "${stage}" "${environment}" "${state_prefix}" "${repo_root}" >/dev/null

printf 'Rebound the continuously held %s recovery lease from source %s to reviewed descendant %s.\n' \
  "${stage}" "${old_source_head}" "${source_head}"
