#!/usr/bin/env bash
set -euo pipefail

plan_path="${1:?usage: assert-workload-plan.sh PLAN_PATH TERRAFORM_BIN POLICY_PATH PACKER_TEMPLATE_PATH ARTIFACTS_PATH [full|cluster]}"
terraform_bin="${2:-terraform}"
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
policy_path="${3:-${script_dir}/../topology/minimal-workload-policy.json}"
packer_template_path="${4:-${script_dir}/../nomad-cluster-disk-image/main.pkr.hcl}"
artifacts_path="${5:?usage: assert-workload-plan.sh PLAN_PATH TERRAFORM_BIN POLICY_PATH PACKER_TEMPLATE_PATH ARTIFACTS_PATH [full|cluster]}"
scope="${6:-full}"
analysis_filter="${script_dir}/workload-plan-topology.jq"
artifact_filter="${script_dir}/workload-plan-artifacts.jq"

case "${scope}" in
  full | cluster) ;;
  *)
    printf 'Unknown workload plan assertion scope: %s\n' "${scope}" >&2
    exit 2
    ;;
esac

command -v jq >/dev/null 2>&1 || {
  printf 'jq is required to inspect the saved workload plan.\n' >&2
  exit 1
}

nomad_bin=""
if [[ "${scope}" == "full" ]]; then
  nomad_candidate="${NOMAD_BIN:-nomad}"
  if [[ "${nomad_candidate}" == */* ]]; then
    [[ -x "${nomad_candidate}" ]] || {
      printf 'Pinned Nomad CLI is not executable: %s\n' "${nomad_candidate}" >&2
      exit 1
    }
    nomad_bin="${nomad_candidate}"
  else
    nomad_bin="$(command -v "${nomad_candidate}" 2>/dev/null || true)"
    [[ -n "${nomad_bin}" ]] || {
      printf 'Pinned Nomad CLI is required to inspect workload jobspecs.\n' >&2
      exit 1
    }
  fi

  nomad_version_output="$("${nomad_bin}" version 2>/dev/null)" || {
    printf 'Unable to verify the pinned Nomad CLI version.\n' >&2
    exit 1
  }
  nomad_version_line="${nomad_version_output%%$'\n'*}"
  [[ "${nomad_version_line}" == "Nomad v1.8.4" ]] || {
    printf 'Nomad CLI version must be exactly 1.8.4; found: %s\n' \
      "${nomad_version_line}" >&2
    exit 1
  }
fi

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
  def positive_integer:
    type == "number" and . > 0 and floor == .;

  def gcs_object:
    (keys | sort)
      == ["bucket", "crc32c", "generation", "md5", "name", "size"]
    and (.bucket | type) == "string"
    and (.bucket | length) > 0
    and (.name | type) == "string"
    and (.name | length) > 0
    and (.generation | type) == "string"
    and (.generation | test("^[1-9][0-9]*$"))
    and (.size | positive_integer)
    and (.md5 | type) == "string"
    and (.md5 | test("^[A-Za-z0-9+/]{22}==$"))
    and (.crc32c | type) == "string"
    and (.crc32c | test("^[A-Za-z0-9+/]{6}==$"));

  (.core_images | keys | sort) == [
    "api",
    "clickhouse-migrator",
    "client-proxy",
    "db-migrator",
    "docker-reverse-proxy"
  ]
  and .schema_version == 2
  and (.gcp_project_id | type) == "string"
  and (.gcp_region | type) == "string"
  and (.core_repository | type) == "string"
  and (.core_image_revision | type) == "string"
  and (.core_image_revision | test("^[0-9a-f]{12,40}$"))
  and (.job_binary_bucket | type) == "string"
  and (.job_binary_bucket | length) > 0
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
  and (.job_binaries | keys | sort) == [
    "clean-nfs-cache",
    "envd",
    "orchestrator",
    "template-manager"
  ]
  and all(
    .job_binaries
    | to_entries[];
    .key as $name
    | .value.canonical as $canonical
    | .value.revision as $revision
    | ($canonical | gcs_object)
    and ($revision | gcs_object)
    and $canonical.bucket == $input.job_binary_bucket
    and $canonical.name == $name
    and $revision.bucket == $input.job_binary_bucket
    and $revision.name == ($name + "." + $input.core_image_revision)
    and $canonical.size == $revision.size
    and $canonical.md5 == $revision.md5
    and $canonical.crc32c == $revision.crc32c
    and .value.nomad_source == (
      "gcs::https://www.googleapis.com/storage/v1/"
      + $input.job_binary_bucket
      + "/"
      + $revision.name
      + "#"
      + $revision.generation
    )
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
      "terraform_data.cloud_sql_connection_budget",
      "time_sleep.service_identity_propagation"
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

if [[ "${scope}" == "full" ]]; then
  cluster_compute_mutations="$(
    jq -c '
      [
        .resource_changes[]?
        | select(.mode == "managed")
        | select(.address | startswith("module.cluster."))
        | select(.type | startswith("google_compute_"))
        | select(
            .change.actions != ["no-op"]
            and .change.actions != ["read"]
          )
        | {
            address,
            type,
            actions: .change.actions
          }
      ]
    ' <<<"${plan_json}"
  )"
  if [[ "$(jq 'length' <<<"${cluster_compute_mutations}")" -ne 0 ]]; then
    printf 'Refusing phase-two workload plan: module.cluster compute mutations must be empty.\n' >&2
    jq -c '.[]' <<<"${cluster_compute_mutations}" >&2
    exit 1
  fi
fi

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
  invalid_single_unavailable_regional_migs
  automated_worker_server_surges
  unresolved_templates
  invalid_template_disks
)

if [[ "${scope}" == "full" ]]; then
  failure_fields+=(
    destructive_cloud_sql_resources
    unknown_cloud_sql_resources
    missing_or_duplicate_cloud_sql_resources
    invalid_cloud_sql_resources
  )
fi
failure_fields+=(
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

if [[ "${scope}" == "cluster" ]]; then
  unexpected_nomad_resources="$(
    jq -c '
      [
        .resource_changes[]?
        | select(
            (.type | startswith("nomad_"))
            or (.address | startswith("module.nomad."))
          )
        | {
            address,
            type,
            actions: .change.actions
          }
      ]
    ' <<<"${plan_json}"
  )"
  if [[ "$(jq 'length' <<<"${unexpected_nomad_resources}")" -ne 0 ]]; then
    printf 'Refusing cluster bootstrap plan: Nomad workload resources must be absent.\n' >&2
    jq -c '.[]' <<<"${unexpected_nomad_resources}" >&2
    exit 1
  fi

  unexpected_cluster_mutations="$(
    jq -c '
      [
        .resource_changes[]?
        | select(.mode == "managed")
        | select(
            .change.actions != ["no-op"]
            and .change.actions != ["read"]
          )
        | select(.address | startswith("module.cluster.") | not)
        | {
            address,
            type,
            actions: .change.actions
          }
      ]
    ' <<<"${plan_json}"
  )"
  if [[ "$(jq 'length' <<<"${unexpected_cluster_mutations}")" -ne 0 ]]; then
    printf 'Refusing cluster bootstrap plan: mutations outside module.cluster must be empty.\n' >&2
    jq -c '.[]' <<<"${unexpected_cluster_mutations}" >&2
    exit 1
  fi
fi

core_job_images='{}'
if [[ "${scope}" == "full" ]]; then
  core_job_specs="$(
    jq -cn '[
      "module.nomad.module.api.nomad_job.api",
      "module.nomad.nomad_job.docker_reverse_proxy",
      "module.nomad.module.client_proxy.nomad_job.client_proxy"
    ]'
  )"
  temp_root="${TMPDIR:-/tmp}"
  core_jobs_dir="$(mktemp -d "${temp_root%/}/workload-core-jobs.XXXXXX")"
  chmod 0700 "${core_jobs_dir}"
  cleanup_core_jobs() {
    rm -rf -- "${core_jobs_dir}"
  }
  trap cleanup_core_jobs EXIT

  core_job_index=0
  while IFS= read -r core_job_address; do
    core_job_index=$((core_job_index + 1))
    core_job_row_count="$(
      jq \
        --arg address "${core_job_address}" '
        [
          .resource_changes[]?
          | select(.address == $address)
        ]
        | length
      ' <<<"${plan_json}"
    )"
    if [[ "${core_job_row_count}" -ne 1 ]]; then
      printf 'Refusing workload plan: missing_or_duplicate_core_jobs must be empty.\n' >&2
      exit 1
    fi

    if ! jq -e \
      --arg address "${core_job_address}" '
      [
        .resource_changes[]?
        | select(.address == $address)
      ] as $rows
      | $rows[0].mode == "managed"
        and $rows[0].type == "nomad_job"
        and ($rows[0].change.after.jobspec | type) == "string"
        and ($rows[0].change.after.jobspec | length) > 0
        and ($rows[0].change.after_unknown.jobspec // false) != true
    ' <<<"${plan_json}" >/dev/null; then
      printf 'Refusing workload plan: invalid_core_jobs must be empty. Invalid row: %s.\n' \
        "${core_job_address}" >&2
      exit 1
    fi

    jobspec_path="${core_jobs_dir}/job-${core_job_index}.hcl"
    rendered_job_path="${core_jobs_dir}/job-${core_job_index}.json"
    jq -er \
      --arg address "${core_job_address}" '
      .resource_changes[]
      | select(.address == $address)
      | .change.after.jobspec
    ' <<<"${plan_json}" >"${jobspec_path}"
    chmod 0600 "${jobspec_path}"

    if ! "${nomad_bin}" job run -output "${jobspec_path}" \
      >"${rendered_job_path}" 2>/dev/null; then
      printf 'Refusing workload plan: invalid_core_jobs must be empty. Unparseable jobspec: %s.\n' \
        "${core_job_address}" >&2
      exit 1
    fi
    chmod 0600 "${rendered_job_path}"

    if ! rendered_images="$(
      jq -ce '
        [
          .Job.TaskGroups[]?.Tasks[]?
        ] as $tasks
        | [
            (
              .Job.TaskGroups[]?.Services[]?.Connect?
            ),
            (
              .Job.TaskGroups[]?.Tasks[]?.Services[]?.Connect?
            )
            | select(
                . != null
                and (
                  type != "object"
                  or length > 0
                )
              )
          ] as $connect_declarations
        | if (
            ($tasks | length) > 0
            and ($connect_declarations | length) == 0
            and all(
              $tasks[];
              .Driver == "docker"
              and (.Config.image | type) == "string"
              and (.Config.image | length) > 0
            )
          ) then
            [$tasks[].Config.image]
          else
            error("invalid core task driver, Docker image, or Connect declaration")
          end
      ' "${rendered_job_path}"
    )"; then
      printf 'Refusing workload plan: invalid_core_jobs must be empty. Invalid core task driver, image, or Connect declaration: %s.\n' \
        "${core_job_address}" >&2
      exit 1
    fi

    core_job_images="$(
      jq -cn \
        --argjson current "${core_job_images}" \
        --arg address "${core_job_address}" \
        --argjson images "${rendered_images}" '
        $current + {($address): $images}
      '
    )"
  done < <(jq -r '.[]' <<<"${core_job_specs}")
fi

artifact_bindings="$(
  jq -c \
    --argjson artifacts "${artifacts_json}" \
    --argjson core_job_images "${core_job_images}" \
    -f "${artifact_filter}" \
    <<<"${plan_json}"
)"
artifact_failure_fields=(
  missing_or_duplicate_orchestrator_images
  invalid_orchestrator_images
  invalid_template_source_images
)
if [[ "${scope}" == "full" ]]; then
  artifact_failure_fields+=(
    missing_or_duplicate_core_images
    invalid_core_images
    missing_or_duplicate_core_jobs
    invalid_core_jobs
    missing_or_duplicate_job_binary_objects
    invalid_job_binary_objects
    missing_or_duplicate_job_binary_jobs
    invalid_job_binary_jobs
  )
fi

for field in "${artifact_failure_fields[@]}"; do
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
if [[ "${scope}" == "cluster" ]]; then
  printf 'Cluster bootstrap scope passed: only module.cluster may mutate and no Nomad workload resources are present.\n'
fi
