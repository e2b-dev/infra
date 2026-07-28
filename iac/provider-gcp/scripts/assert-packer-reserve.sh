#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
policy_path="${1:-${script_dir}/../topology/minimal-workload-policy.json}"
packer_template_path="${2:-${script_dir}/../nomad-cluster-disk-image/main.pkr.hcl}"

command -v jq >/dev/null 2>&1 || {
  printf 'jq is required to verify the Packer quota reserve.\n' >&2
  exit 1
}

[[ -f "${policy_path}" ]] || {
  printf 'Workload topology policy does not exist: %s\n' "${policy_path}" >&2
  exit 1
}

[[ -f "${packer_template_path}" ]] || {
  printf 'Packer template does not exist: %s\n' "${packer_template_path}" >&2
  exit 1
}

reviewed_reserve="$(
  jq -cnS '{
    machine_type: "n1-standard-4",
    instances: 1,
    vcpus: 4,
    disk_type: "pd-ssd",
    pd_ssd_gb: 10,
    pd_standard_gb: 0,
    local_ssd_gb: 0,
    regional_public_ips: 1
  }'
)"
policy_reserve="$(jq -cS '.transient_reserve' "${policy_path}")"

if [[ "${policy_reserve}" != "${reviewed_reserve}" ]]; then
  printf 'Packer quota reserve differs from the reviewed static reserve.\n' >&2
  printf 'Reviewed: %s\n' "${reviewed_reserve}" >&2
  printf 'Policy:   %s\n' "${policy_reserve}" >&2
  exit 1
fi

normalized_template="$(sed -E 's/[[:space:]]+//g' "${packer_template_path}")"

require_assignment() {
  local assignment="$1"
  local description="$2"
  local count

  count="$(
    grep -Fxc "${assignment}" <<<"${normalized_template}" || true
  )"
  if [[ "${count}" -ne 1 ]]; then
    printf 'Packer template must contain exactly one %s assignment: %s\n' \
      "${description}" "${assignment}" >&2
    exit 1
  fi
}

require_assignment \
  'quota_policy=jsondecode(file(abspath("${path.root}/../topology/minimal-workload-policy.json")))' \
  'quota-policy source'
require_assignment \
  'quota_reserve=local.quota_policy.transient_reserve' \
  'quota-reserve binding'
require_assignment \
  'disk_size=local.quota_reserve.pd_ssd_gb' \
  'disk-size reserve'
require_assignment \
  'disk_type=local.quota_reserve.disk_type' \
  'disk-type reserve'
require_assignment \
  'machine_type=local.quota_reserve.machine_type' \
  'machine-type reserve'
require_assignment \
  'omit_external_ip=local.quota_reserve.regional_public_ips==0' \
  'public-IP reserve'
require_assignment \
  'use_iap=true' \
  'IAP setting'

printf 'Packer configuration matches the reviewed one-VM quota reserve.\n'
