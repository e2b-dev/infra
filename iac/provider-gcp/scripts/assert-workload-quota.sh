#!/usr/bin/env bash
set -euo pipefail

policy_path="${1:?usage: assert-workload-quota.sh POLICY PROJECT REGION [GCLOUD_BIN] [bootstrap|post-cluster]}"
project_id="${2:?usage: assert-workload-quota.sh POLICY PROJECT REGION [GCLOUD_BIN] [bootstrap|post-cluster]}"
region="${3:?usage: assert-workload-quota.sh POLICY PROJECT REGION [GCLOUD_BIN] [bootstrap|post-cluster]}"
gcloud_bin="${4:-gcloud}"
quota_mode="${5:-bootstrap}"

case "${quota_mode}" in
  bootstrap | post-cluster) ;;
  *)
    printf 'Unknown workload quota mode: %s\n' "${quota_mode}" >&2
    exit 2
    ;;
esac

command -v jq >/dev/null 2>&1 || {
  printf 'jq is required to inspect live workload quota.\n' >&2
  exit 1
}

[[ -f "${policy_path}" && ! -L "${policy_path}" ]] || {
  printf 'Workload topology policy must be a regular, non-symlink file: %s\n' \
    "${policy_path}" >&2
  exit 1
}
if [[ ! -x "${gcloud_bin}" ]] && ! command -v "${gcloud_bin}" >/dev/null 2>&1; then
  printf 'gcloud is not installed or executable: %s\n' "${gcloud_bin}" >&2
  exit 1
fi

project_json="$(
  "${gcloud_bin}" compute project-info describe \
    --project="${project_id}" \
    --format=json
)" || {
  printf 'Unable to read global Compute Engine quota for project %s.\n' \
    "${project_id}" >&2
  exit 1
}
region_json="$(
  "${gcloud_bin}" compute regions describe "${region}" \
    --project="${project_id}" \
    --format=json
)" || {
  printf 'Unable to read regional Compute Engine quota for %s/%s.\n' \
    "${project_id}" "${region}" >&2
  exit 1
}

quota_value() {
  local document="$1"
  local metric="$2"
  local scope="$3"

  jq -cer \
    --arg metric "${metric}" \
    --arg scope "${scope}" '
      [
        .quotas[]?
        | select(.metric == $metric)
        | select(
            (.limit | type) == "number"
            and (.usage | type) == "number"
            and .limit >= -1
            and .usage >= 0
            and (.limit == -1 or .usage <= .limit)
          )
        | {
            reported_limit: .limit,
            effective_limit: (
              if .limit == -1 then 1e18 else .limit end
            ),
            unlimited: (.limit == -1),
            usage
          }
      ]
      | if length == 1
        then .[0]
        else error(
          "expected exactly one valid "
          + $scope
          + " quota metric "
          + $metric
        )
        end
    ' <<<"${document}"
}

if ! global_cpu="$(
  quota_value "${project_json}" "CPUS_ALL_REGIONS" "global"
)"; then
  # Newer Compute quota responses no longer expose CPUS_ALL_REGIONS. In that
  # response shape, regional CPUS is the authoritative capacity limit and is
  # deliberately reused for the aggregate planning dimension.
  global_cpu="$(quota_value "${region_json}" "CPUS" "regional fallback")" || {
    printf 'Live quota response is missing both CPUS_ALL_REGIONS and a unique valid regional CPUS metric.\n' >&2
    exit 1
  }
fi

live_json="$(
  jq -cn \
    --argjson global_vcpus "${global_cpu}" \
    '{global_vcpus: $global_vcpus}'
)"
for quota_name in \
  instances \
  regional_cpus \
  pd_ssd_gb \
  pd_standard_gb \
  local_ssd_gb \
  regional_public_ips; do
  case "${quota_name}" in
    instances) metric="INSTANCES" ;;
    regional_cpus) metric="CPUS" ;;
    pd_ssd_gb) metric="SSD_TOTAL_GB" ;;
    pd_standard_gb) metric="DISKS_TOTAL_GB" ;;
    local_ssd_gb) metric="LOCAL_SSD_TOTAL_GB" ;;
    regional_public_ips) metric="IN_USE_ADDRESSES" ;;
    *)
      printf 'Unknown workload quota policy key: %s\n' "${quota_name}" >&2
      exit 1
      ;;
  esac
  value="$(quota_value "${region_json}" "${metric}" "regional")" || {
    printf 'Live quota response is missing a unique valid %s metric.\n' "${metric}" >&2
    exit 1
  }
  live_json="$(
    jq -c \
      --arg key "${quota_name}" \
      --argjson value "${value}" \
      '. + {($key): $value}' <<<"${live_json}"
  )"
done

policy_json="$(jq -ce . "${policy_path}")"
post_cluster_required="$(
  jq -ce '
    . as $policy
    | (
        reduce (
          $policy.expected_role_max_instances
          | to_entries[]
        ) as $role (
          {
            instances: 0,
            global_vcpus: 0,
            regional_cpus: 0,
            pd_ssd_gb: 0,
            pd_standard_gb: 0,
            local_ssd_gb: 0,
            regional_public_ips: 0
          };
          .instances += $role.value
          | .global_vcpus += (
              $role.value
              * $policy.expected_role_resources[$role.key].vcpus
            )
          | .regional_cpus = .global_vcpus
          | .pd_ssd_gb += (
              $role.value
              * $policy.expected_role_resources[$role.key].pd_ssd_gb
            )
          | .pd_standard_gb += (
              $role.value
              * $policy.expected_role_resources[$role.key].pd_standard_gb
            )
          | .local_ssd_gb += (
              $role.value
              * $policy.expected_role_resources[$role.key].local_ssd_gb
            )
          | .regional_public_ips += (
              $role.value
              * (
                  if $policy.expected_role_resources[$role.key].regional_public_ip
                  then 1
                  else 0
                  end
                )
              )
        )
        | .regional_public_ips += (
            $policy.fixed_regional_public_ip_addresses | length
          )
      ) as $base
    | $policy.expected_peak_usage as $peak
    | if (
        ($base | keys | sort) == ($peak | keys | sort)
        and all($base[]; type == "number" and . >= 0 and floor == .)
        and all($peak[]; type == "number" and . >= 0 and floor == .)
      )
      then reduce ($peak | to_entries[]) as $entry (
        {};
        .[$entry.key] = ($entry.value - $base[$entry.key])
      )
      else error("invalid workload quota policy shape")
      end
    | if all(.[]; type == "number" and . >= 0 and floor == .)
      then .
      else error("workload peak is below the deployed base fleet")
      end
  ' <<<"${policy_json}"
)" || {
  printf 'Workload topology policy cannot derive post-cluster reserve headroom.\n' >&2
  exit 1
}

report="$(
  jq -cn \
    --arg project_id "${project_id}" \
    --arg region "${region}" \
    --arg quota_mode "${quota_mode}" \
    --argjson live "${live_json}" \
    --argjson bootstrap_required "$(jq -c '.expected_peak_usage' <<<"${policy_json}")" \
    --argjson post_cluster_required "${post_cluster_required}" \
    --argjson reviewed "$(jq -c '.quota_limits' <<<"${policy_json}")" '
      (
        if $quota_mode == "bootstrap"
        then $bootstrap_required
        else $post_cluster_required
        end
      ) as $required
      |
      {
        project_id: $project_id,
        region: $region,
        quota_mode: $quota_mode,
        quotas: (
          reduce ($required | keys[]) as $key (
            {};
            .[$key] = {
              metric_limit: (
                if $live[$key].unlimited
                then "unlimited"
                else $live[$key].reported_limit
                end
              ),
              effective_limit: $live[$key].effective_limit,
              current_usage: $live[$key].usage,
              available: (
                if $live[$key].unlimited
                then "unlimited"
                else ($live[$key].reported_limit - $live[$key].usage)
                end
              ),
              effective_available: (
                $live[$key].effective_limit - $live[$key].usage
              ),
              required_headroom: $required[$key],
              reviewed_limit: $reviewed[$key]
            }
          )
        )
      }
      | .violations = [
          .quotas
          | to_entries[]
          | select(
              .value.effective_limit < .value.reviewed_limit
              or .value.effective_available < .value.required_headroom
            )
          | {
              quota: .key,
              live_limit: .value.metric_limit,
              reviewed_limit: .value.reviewed_limit,
              current_usage: .value.current_usage,
              available: .value.available,
              required_headroom: .value.required_headroom
            }
        ]
    '
)"

if [[ "$(jq '.violations | length' <<<"${report}")" -ne 0 ]]; then
  printf 'Refusing one-workcell canary: live quota headroom is insufficient.\n' >&2
  jq -c '.violations[]' <<<"${report}" >&2
  exit 1
fi

printf 'Live one-workcell quota passed for %s/%s in %s mode: %s\n' \
  "${project_id}" \
  "${region}" \
  "${quota_mode}" \
  "$(jq -c '.quotas' <<<"${report}")"
