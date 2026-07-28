#!/usr/bin/env bash
set -euo pipefail

plan_path="${1:?usage: assert-workload-plan.sh PLAN_PATH TERRAFORM_BIN POLICY_PATH PACKER_TEMPLATE_PATH ARTIFACTS_PATH}"
terraform_bin="${2:-terraform}"
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
policy_path="${3:-${script_dir}/../topology/minimal-workload-policy.json}"
packer_template_path="${4:-${script_dir}/../nomad-cluster-disk-image/main.pkr.hcl}"
artifacts_path="${5:?usage: assert-workload-plan.sh PLAN_PATH TERRAFORM_BIN POLICY_PATH PACKER_TEMPLATE_PATH ARTIFACTS_PATH}"
analysis_filter="${script_dir}/workload-plan-topology.jq"
artifact_filter="${script_dir}/workload-plan-artifacts.jq"

command -v jq >/dev/null 2>&1 || {
  printf 'jq is required to inspect the saved workload plan.\n' >&2
  exit 1
}

[[ -f "${plan_path}" ]] || {
  printf 'Saved workload plan does not exist: %s\n' "${plan_path}" >&2
  exit 1
}

[[ -f "${policy_path}" ]] || {
  printf 'Workload topology policy does not exist: %s\n' "${policy_path}" >&2
  exit 1
}

[[ -f "${analysis_filter}" ]] || {
  printf 'Workload topology analysis filter does not exist: %s\n' \
    "${analysis_filter}" >&2
  exit 1
}

[[ -f "${artifact_filter}" ]] || {
  printf 'Workload artifact analysis filter does not exist: %s\n' \
    "${artifact_filter}" >&2
  exit 1
}

[[ -f "${artifacts_path}" && ! -L "${artifacts_path}" ]] || {
  printf 'Resolved workload artifacts must be a regular, non-symlink file: %s\n' \
    "${artifacts_path}" >&2
  exit 1
}

artifacts_json="$(jq -ceS '
  (.core_images | keys | sort) == [
    "api",
    "clickhouse-migrator",
    "client-proxy",
    "db-migrator",
    "docker-reverse-proxy"
  ]
  and .schema_version == 1
  and (.gcp_project_id | type) == "string"
  and (.gcp_region | type) == "string"
  and (.core_repository | type) == "string"
  and (.core_image_revision | type) == "string"
  and (.core_image_revision | test("^[0-9a-f]{12,40}$"))
  and .orchestrator_image.family == "e2b-orch"
  and .orchestrator_image.project == .gcp_project_id
  and .orchestrator_image.status == "READY"
  and (.orchestrator_image.name | type) == "string"
  and (.orchestrator_image.self_link | type) == "string"
  and all(
    .core_images[];
    (.revision.reference | type) == "string"
    and (.revision.digest | test("^sha256:[0-9a-f]{64}$"))
    and (.revision.resolved_reference | type) == "string"
    and (.latest.reference | type) == "string"
    and (.latest.digest | test("^sha256:[0-9a-f]{64}$"))
    and (.latest.resolved_reference | type) == "string"
    and .revision.digest == .latest.digest
    and .revision.resolved_reference == .latest.resolved_reference
  )
  |
  if . then $input else error("invalid resolved workload artifacts") end
' --argjson input "$(jq -c . "${artifacts_path}")" "${artifacts_path}")" || {
  printf 'Resolved workload artifacts are invalid: %s\n' "${artifacts_path}" >&2
  exit 1
}

reviewed_quota_limits="$(
  jq -cn '{
    instances: 24,
    global_vcpus: 32,
    regional_cpus: 32,
    pd_ssd_gb: 500,
    pd_standard_gb: 4096,
    local_ssd_gb: 6000,
    regional_public_ips: 8
  }'
)"
reviewed_peak_usage="$(
  jq -cn '{
    instances: 7,
    global_vcpus: 30,
    regional_cpus: 30,
    pd_ssd_gb: 270,
    pd_standard_gb: 400,
    local_ssd_gb: 750,
    regional_public_ips: 7
  }'
)"
reviewed_reserve="$(
  jq -cn '{
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
reviewed_cloud_sql="$(
  jq -cn '{
    resource_addresses: [
      "google_compute_global_address.cloud_sql_private_services",
      "google_project_iam_member.cloud_sql_service_agent",
      "google_project_iam_member.service_networking_service_agent",
      "google_project_service.cloud_sql_admin_api",
      "google_project_service.service_networking_api",
      "google_project_service_identity.cloud_sql",
      "google_project_service_identity.service_networking",
      "google_secret_manager_secret_version.postgres_connection_string",
      "google_service_networking_connection.cloud_sql",
      "google_sql_database.operator_canary",
      "google_sql_database_instance.operator_canary",
      "google_sql_user.operator_canary",
      "random_password.cloud_sql_operator_canary",
      "terraform_data.cloud_sql_connection_budget"
    ],
    instance_name_suffix: "postgres-canary",
    connection_secret_id_suffix: "postgres-connection-string",
    database_version: "POSTGRES_16",
    tier: "db-f1-micro",
    edition: "ENTERPRISE",
    availability_type: "ZONAL",
    disk_type: "PD_HDD",
    disk_size_gb: 10,
    disk_autoresize_limit_gb: 20,
    private_services_prefix_length: 24,
    ssl_mode: "ENCRYPTED_ONLY",
    backup_start_time: "03:00",
    retained_backups: 7,
    transaction_log_retention_days: 7,
    database_name: "e2b",
    user_name: "e2b",
    application_connection_budget: 19,
    migrator_max_open_connections: 4,
    docker_reverse_proxy_max_open_connections: 6,
    dashboard_api_max_open_connections_per_instance: 16,
    api_server_count: 1,
    dashboard_api_count: 0
  }'
)"
policy_json="$(jq -c . "${policy_path}")"

jq -e \
  --argjson reviewed_quota_limits "${reviewed_quota_limits}" \
  --argjson reviewed_peak_usage "${reviewed_peak_usage}" \
  --argjson reviewed_reserve "${reviewed_reserve}" \
  --argjson reviewed_cloud_sql "${reviewed_cloud_sql}" '
  def machine_vcpus($machine_type):
    try (
      $machine_type
      | capture("^(?:e2|n1)-standard-(?<vcpus>[1-9][0-9]*)$")
      | .vcpus
      | tonumber
    ) catch null;

  def nonnegative_integer:
    type == "number" and . >= 0 and floor == .;

  def quota_usage_shape:
    (keys | sort)
      == [
        "global_vcpus",
        "instances",
        "local_ssd_gb",
        "pd_ssd_gb",
        "pd_standard_gb",
        "regional_cpus",
        "regional_public_ips"
      ]
    and all(.[]; nonnegative_integer);

  def role_resources_shape:
    (keys | sort)
      == [
        "local_ssd_gb",
        "machine_type",
        "pd_ssd_gb",
        "pd_standard_gb",
        "regional_public_ip",
        "vcpus"
      ]
    and (.machine_type | type) == "string"
    and (.vcpus | nonnegative_integer)
    and .vcpus > 0
    and machine_vcpus(.machine_type) == .vcpus
    and (.pd_ssd_gb | nonnegative_integer)
    and (.pd_standard_gb | nonnegative_integer)
    and (.local_ssd_gb | nonnegative_integer)
    and (.regional_public_ip | type) == "boolean";

  (.expected_role_max_instances | keys | sort)
    == ["api", "build", "clickhouse", "client", "loki", "server"]
  and (
    .expected_role_max_instances
    | all(.[]; nonnegative_integer)
  )
  and (.expected_role_surge_instances | keys | sort)
    == ["api", "build", "clickhouse", "client", "loki", "server"]
  and (
    .expected_role_surge_instances
    | all(.[]; nonnegative_integer)
  )
  and (.expected_role_max_unavailable_instances | keys | sort)
    == ["api", "build", "clickhouse", "client", "loki", "server"]
  and (
    .expected_role_max_unavailable_instances
    | all(.[]; nonnegative_integer)
  )
  and (.expected_role_resources | keys | sort)
    == ["api", "build", "clickhouse", "client", "loki", "server"]
  and (
    .expected_role_resources
    | all(.[]; role_resources_shape)
  )
  and .transient_reserve == $reviewed_reserve
  and .transient_scenarios_are_mutually_exclusive == true
  and .expected_peak_usage == $reviewed_peak_usage
  and (.expected_peak_usage | quota_usage_shape)
  and .expected_cloud_sql == $reviewed_cloud_sql
  and .quota_limits == $reviewed_quota_limits
  and (.quota_limits | quota_usage_shape)
  and (
    .max_automated_worker_server_surge_per_pool
    | nonnegative_integer
  )
  and .max_automated_worker_server_surge_per_pool == 0
' <<<"${policy_json}" >/dev/null || {
  printf 'Workload topology policy is invalid or differs from reviewed quota limits: %s\n' \
    "${policy_path}" >&2
  exit 1
}

"${script_dir}/assert-packer-reserve.sh" \
  "${policy_path}" \
  "${packer_template_path}" >/dev/null

plan_json="$("${terraform_bin}" show -json "${plan_path}")"

jq -e '.errored != true' <<<"${plan_json}" >/dev/null || {
  printf 'Refusing workload plan: Terraform recorded an errored plan.\n' >&2
  exit 1
}

topology="$(
  jq -c \
    --argjson expected "${policy_json}" \
    -f "${analysis_filter}" \
    <<<"${plan_json}"
)"

failure_fields=(
  destructive_migs
  unknown_migs
  unknown_templates
  unexpected_quota_resources
  missing_or_duplicate_mig_roles
  missing_or_duplicate_template_roles
  unresolved_capacities
  unresolved_previous_capacities
  capacity_reductions
  unresolved_surges
  unresolved_max_unavailable
  invalid_surges
  percentage_surges
  invalid_max_unavailable
  automated_worker_server_surges
  unresolved_templates
  invalid_template_disks
  destructive_cloud_sql_resources
  unknown_cloud_sql_resources
  missing_or_duplicate_cloud_sql_resources
  invalid_cloud_sql_resources
  destructive_managed_resources
  quota_violations
)

for field in "${failure_fields[@]}"; do
  if [[ "$(jq ".${field} | length" <<<"${topology}")" -ne 0 ]]; then
    printf 'Refusing workload plan: %s must be empty.\n' "${field}" >&2
    jq -c ".${field}[]" <<<"${topology}" >&2
    exit 1
  fi
done

for comparison in \
  "role maximum instance counts|role_max_instances|expected_role_max_instances" \
  "role rollout surge counts|role_surge_instances|expected_role_surge_instances" \
  "role maximum unavailable counts|role_max_unavailable_instances|expected_role_max_unavailable_instances" \
  "role machine and disk resources|role_resources|expected_role_resources" \
  "peak quota usage|peak_usage|expected_peak_usage"; do
  description="${comparison%%|*}"
  remaining="${comparison#*|}"
  topology_field="${remaining%%|*}"
  policy_field="${remaining#*|}"

  if ! jq -e \
    --argjson expected "$(jq -c ".${policy_field}" <<<"${policy_json}")" \
    ".${topology_field} == \$expected" <<<"${topology}" >/dev/null; then
    printf 'Refusing workload plan: %s differ from policy.\n' \
      "${description}" >&2
    printf 'Expected: %s\n' \
      "$(jq -c ".${policy_field}" <<<"${policy_json}")" >&2
    printf 'Planned:  %s\n' \
      "$(jq -c ".${topology_field}" <<<"${topology}")" >&2
    exit 1
  fi
done

artifact_bindings="$(
  jq -c \
    --argjson artifacts "${artifacts_json}" \
    -f "${artifact_filter}" \
    <<<"${plan_json}"
)"
for field in \
  missing_or_duplicate_orchestrator_images \
  invalid_orchestrator_images \
  invalid_template_source_images \
  missing_or_duplicate_core_images \
  invalid_core_images \
  missing_or_duplicate_core_jobs \
  invalid_core_jobs; do
  if [[ "$(jq ".${field} | length" <<<"${artifact_bindings}")" -ne 0 ]]; then
    printf 'Refusing workload plan: %s must be empty.\n' "${field}" >&2
    jq -c ".${field}[]" <<<"${artifact_bindings}" >&2
    exit 1
  fi
done

printf 'Workload plan topology passed: roles=%s surge=%s unavailable=%s base=%s rollout=%s packer=%s peak=%s limits=%s.\n' \
  "$(jq -c '.role_max_instances' <<<"${topology}")" \
  "$(jq -c '.role_surge_instances' <<<"${topology}")" \
  "$(jq -c '.role_max_unavailable_instances' <<<"${topology}")" \
  "$(jq -c '.base_usage' <<<"${topology}")" \
  "$(jq -c '.rollout_usage' <<<"${topology}")" \
  "$(jq -c '.packer_usage' <<<"${topology}")" \
  "$(jq -c '.peak_usage' <<<"${topology}")" \
  "$(jq -c '.quota_limits' <<<"${policy_json}")"
printf 'The API rollout and Packer reserve are mutually exclusive; verify live quotas immediately before apply.\n'
