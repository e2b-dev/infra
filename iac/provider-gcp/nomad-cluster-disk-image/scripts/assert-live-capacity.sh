#!/usr/bin/env bash
set -euo pipefail

policy_path="${1:?usage: assert-live-capacity.sh POLICY GCLOUD_BIN PROJECT REGION}"
gcloud_bin="${2:?usage: assert-live-capacity.sh POLICY GCLOUD_BIN PROJECT REGION}"
project_id="${3:?usage: assert-live-capacity.sh POLICY GCLOUD_BIN PROJECT REGION}"
region="${4:?usage: assert-live-capacity.sh POLICY GCLOUD_BIN PROJECT REGION}"

[[ -f "${policy_path}" && ! -L "${policy_path}" ]] || {
  printf 'Capacity policy must be a regular, non-symlink file: %s\n' \
    "${policy_path}" >&2
  exit 1
}

project_json="$(
  "${gcloud_bin}" compute project-info describe \
    --project="${project_id}" \
    --format=json
)"
region_json="$(
  "${gcloud_bin}" compute regions describe "${region}" \
    --project="${project_id}" \
    --format=json
)"

if ! jq -e \
  --argjson policy "$(jq -c . "${policy_path}")" \
  --arg project "${project_id}" \
  --arg region "${region}" '
  def quota($quotas; $metric):
    ($quotas | map(select(.metric == $metric)) | first)
      // error("missing quota metric " + $metric);

  .project as $project_doc
  | .region as $region_doc
  | quota($project_doc.quotas; "CPUS_ALL_REGIONS") as $global_cpu
  | quota($region_doc.quotas; "CPUS") as $regional_cpu
  | quota($region_doc.quotas; "INSTANCES") as $instances
  | quota($region_doc.quotas; "SSD_TOTAL_GB") as $ssd
  | quota($region_doc.quotas; "DISKS_TOTAL_GB") as $standard
  | quota($region_doc.quotas; "LOCAL_SSD_TOTAL_GB") as $local_ssd
  | quota($region_doc.quotas; "IN_USE_ADDRESSES") as $addresses
  | ($policy.expected_peak_usage) as $peak
  |
    $project_doc.name == $project
    and $region_doc.name == $region
    and (($global_cpu.limit - $global_cpu.usage) >= $peak.global_vcpus)
    and (($regional_cpu.limit - $regional_cpu.usage) >= $peak.global_vcpus)
    and (($instances.limit - $instances.usage) >= $peak.instances)
    and (($ssd.limit - $ssd.usage) >= $peak.pd_ssd_gb)
    and (($standard.limit - $standard.usage) >= $peak.pd_standard_gb)
    and (($local_ssd.limit - $local_ssd.usage) >= $peak.local_ssd_gb)
    and (
      ($addresses.limit - $addresses.usage)
      >= $peak.regional_public_ips
    )
' <<<"$(jq -cn --argjson project "${project_json}" --argjson region "${region_json}" '{project: $project, region: $region}')" >/dev/null; then
  printf 'Live GCP quota headroom does not preserve the reviewed workload peak plus Packer reserve.\n' >&2
  jq -cn \
    --argjson project "${project_json}" \
    --argjson region "${region_json}" \
    '{
      project_quotas: [
        $project.quotas[]?
        | select(.metric == "CPUS_ALL_REGIONS")
        | {metric, limit, usage}
      ],
      region_quotas: [
        $region.quotas[]?
        | select(
          .metric == "CPUS"
          or .metric == "INSTANCES"
          or .metric == "SSD_TOTAL_GB"
          or .metric == "DISKS_TOTAL_GB"
          or .metric == "LOCAL_SSD_TOTAL_GB"
          or .metric == "IN_USE_ADDRESSES"
        )
        | {metric, limit, usage}
      ]
    }' >&2
  exit 1
fi

printf 'Live GCP quota preserves the full reviewed workload peak and one Packer reserve.\n'
