#!/usr/bin/env bash
set -euo pipefail

plan_path="${1:?usage: assert-workload-prerequisite-plan.sh PLAN_PATH TERRAFORM_BIN POLICY_PATH}"
terraform_bin="${2:-terraform}"
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
policy_path="${3:-${script_dir}/../topology/minimal-workload-policy.json}"
topology_filter="${script_dir}/workload-plan-topology.jq"
expected_project="${WORKLOAD_GCP_PROJECT_ID:?WORKLOAD_GCP_PROJECT_ID is required}"
expected_region="${WORKLOAD_GCP_REGION:?WORKLOAD_GCP_REGION is required}"
expected_prefix="${WORKLOAD_PREFIX:?WORKLOAD_PREFIX is required}"

[[ -f "${plan_path}" ]] || {
  printf 'Saved workload prerequisite plan does not exist: %s\n' "${plan_path}" >&2
  exit 1
}
[[ -f "${policy_path}" ]] || {
  printf 'Workload topology policy does not exist: %s\n' "${policy_path}" >&2
  exit 1
}
[[ -f "${topology_filter}" ]] || {
  printf 'Workload topology analysis filter does not exist: %s\n' "${topology_filter}" >&2
  exit 1
}
command -v jq >/dev/null 2>&1 || {
  printf 'jq is required to inspect the saved prerequisite plan.\n' >&2
  exit 1
}

plan_json="$("${terraform_bin}" show -json "${plan_path}")"
policy_json="$(jq -c . "${policy_path}")"

expected_cloud_sql_policy="$(
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

if ! jq -ne \
  --argjson policy "${policy_json}" \
  --argjson expected "${expected_cloud_sql_policy}" \
  '$policy.expected_cloud_sql == $expected' >/dev/null; then
  printf 'Refusing workload prerequisite plan: Cloud SQL policy must match the exact reviewed contract.\n' >&2
  exit 1
fi

expected_mutations="$(
  jq -cn '[
    {address:"google_artifact_registry_repository.custom_environments_repository",type:"google_artifact_registry_repository",actions:["create"]},
    {address:"google_artifact_registry_repository_iam_member.custom_environments_repository_member",type:"google_artifact_registry_repository_iam_member",actions:["create"]},
    {address:"google_compute_global_address.cloud_sql_private_services",type:"google_compute_global_address",actions:["create"]},
    {address:"google_project_iam_member.cloud_sql_service_agent",type:"google_project_iam_member",actions:["create"]},
    {address:"google_project_iam_member.service_networking_service_agent",type:"google_project_iam_member",actions:["create"]},
    {address:"google_project_service.cloud_sql_admin_api",type:"google_project_service",actions:["create"]},
    {address:"google_project_service.service_networking_api",type:"google_project_service",actions:["create"]},
    {address:"google_project_service_identity.cloud_sql",type:"google_project_service_identity",actions:["create"]},
    {address:"google_project_service_identity.service_networking",type:"google_project_service_identity",actions:["create"]},
    {address:"google_secret_manager_secret.postgres_read_replica_connection_string",type:"google_secret_manager_secret",actions:["create"]},
    {address:"google_secret_manager_secret.sandbox_access_token_hash_seed",type:"google_secret_manager_secret",actions:["create"]},
    {address:"google_secret_manager_secret_version.postgres_connection_string",type:"google_secret_manager_secret_version",actions:["create"]},
    {address:"google_secret_manager_secret_version.postgres_read_replica_connection_string",type:"google_secret_manager_secret_version",actions:["create"]},
    {address:"google_secret_manager_secret_version.sandbox_access_token_hash_seed",type:"google_secret_manager_secret_version",actions:["create"]},
    {address:"google_service_networking_connection.cloud_sql",type:"google_service_networking_connection",actions:["create"]},
    {address:"google_sql_database.operator_canary",type:"google_sql_database",actions:["create"]},
    {address:"google_sql_database_instance.operator_canary",type:"google_sql_database_instance",actions:["create"]},
    {address:"google_sql_user.operator_canary",type:"google_sql_user",actions:["create"]},
    {address:"random_password.cloud_sql_operator_canary",type:"random_password",actions:["create"]},
    {address:"random_password.sandbox_access_token_hash_seed",type:"random_password",actions:["create"]},
    {address:"terraform_data.cloud_sql_connection_budget",type:"terraform_data",actions:["create"]},
    {address:"time_static.volume_token_generation",type:"time_static",actions:["create"]},
    {address:"tls_private_key.volume_token[0]",type:"tls_private_key",actions:["create"]}
  ] | sort_by(.address)'
)"

actual_mutations="$(
  jq -c '
    [
      .resource_changes[]?
      | select(.mode == "managed")
      | select(.change.actions != ["no-op"])
      | {
          address,
          type,
          actions: .change.actions
        }
    ]
    | sort_by(.address)
  ' <<<"${plan_json}"
)"

if ! jq -ne \
  --argjson expected "${expected_mutations}" \
  --argjson actual "${actual_mutations}" \
  '$actual == $expected' >/dev/null; then
  printf 'Refusing workload prerequisite plan: mutation set must be the exact reviewed 23 creates.\n' >&2
  printf 'Expected: %s\n' "$(jq -c . <<<"${expected_mutations}")" >&2
  printf 'Planned:  %s\n' "$(jq -c . <<<"${actual_mutations}")" >&2
  exit 1
fi

unexpected_data_changes="$(
  jq -c '
    [
      .resource_changes[]?
      | select(.mode == "data")
      | {
          address,
          type,
          actions: .change.actions
        }
    ]
  ' <<<"${plan_json}"
)"
if [[ "$(jq 'length' <<<"${unexpected_data_changes}")" -ne 0 ]]; then
  printf 'Refusing workload prerequisite plan: deferred data reads must be empty.\n' >&2
  jq -c '.[]' <<<"${unexpected_data_changes}" >&2
  exit 1
fi

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
  printf 'Refusing workload prerequisite plan: Nomad resources must be absent.\n' >&2
  jq -c '.[]' <<<"${unexpected_nomad_resources}" >&2
  exit 1
fi

topology="$(
  jq -c \
    --argjson expected "${policy_json}" \
    -f "${topology_filter}" \
    <<<"${plan_json}"
)"
for field in \
  destructive_cloud_sql_resources \
  unknown_cloud_sql_resources \
  missing_or_duplicate_cloud_sql_resources \
  invalid_cloud_sql_resources; do
  if [[ "$(jq ".${field} | length" <<<"${topology}")" -ne 0 ]]; then
    printf 'Refusing workload prerequisite plan: %s must be empty.\n' "${field}" >&2
    jq -c ".${field}[]" <<<"${topology}" >&2
    exit 1
  fi
done

if ! jq -e \
  --arg expected_project "${expected_project}" \
  --arg expected_region "${expected_region}" \
  --arg expected_prefix "${expected_prefix}" '
  def row($address):
    [
      .resource_changes[]?
      | select(.address == $address)
    ][0];
  def config($address):
    [
      .configuration.root_module.resources[]?
      | select(.address == $address)
    ][0];

  row("google_artifact_registry_repository.custom_environments_repository") as $repo
  | row("google_artifact_registry_repository_iam_member.custom_environments_repository_member") as $repo_iam
  | row("google_secret_manager_secret.postgres_read_replica_connection_string") as $read_secret
  | row("google_secret_manager_secret_version.postgres_read_replica_connection_string") as $read_version
  | row("random_password.sandbox_access_token_hash_seed") as $seed_password
  | row("google_secret_manager_secret.sandbox_access_token_hash_seed") as $seed_secret
  | row("google_secret_manager_secret_version.sandbox_access_token_hash_seed") as $seed_version
  | row("tls_private_key.volume_token[0]") as $volume_key
  | row("time_static.volume_token_generation") as $volume_time
  | row("google_sql_database_instance.operator_canary") as $sql_instance
  | config("google_artifact_registry_repository.custom_environments_repository") as $repo_config
  | config("google_artifact_registry_repository_iam_member.custom_environments_repository_member") as $repo_iam_config
  | config("google_secret_manager_secret_version.postgres_connection_string") as $connection_version_config
  | config("google_secret_manager_secret_version.postgres_read_replica_connection_string") as $read_version_config
  | config("google_secret_manager_secret_version.sandbox_access_token_hash_seed") as $seed_version_config
  | (
      .configuration.provider_config[
        ($repo_config.provider_config_key // "")
      ] // {}
    ) as $provider_config
  | $repo.change.after.format == "DOCKER"
    and $repo.change.after.project == $expected_project
    and $repo.change.after.location == $expected_region
    and $repo.change.after.repository_id
      == ($expected_prefix + "custom-environments")
    and $repo.change.after_unknown.location != true
    and $repo.change.after_unknown.repository_id != true
    and $repo_config.mode == "managed"
    and $repo_config.type == "google_artifact_registry_repository"
    and $repo_config.expressions.project.references
      == ["var.gcp_project_id"]
    and $repo_config.expressions.location.references
      == ["var.gcp_region"]
    and $provider_config.full_name
      == "registry.terraform.io/hashicorp/google"
    and ($provider_config.alias // null) == null
    and $provider_config.expressions.project.references
      == ["var.gcp_project_id"]
    and $provider_config.expressions.region.references
      == ["var.gcp_region"]
    and $repo_iam.change.after.project == $expected_project
    and $repo_iam.change.after.location == $expected_region
    and $repo_iam.change.after.repository
      == ($expected_prefix + "custom-environments")
    and $repo_iam.change.after.role == "roles/artifactregistry.repoAdmin"
    and $repo_iam.change.after.member
      == (
        "serviceAccount:"
        + $expected_prefix
        + "infra-instances@"
        + $expected_project
        + ".iam.gserviceaccount.com"
      )
    and $repo_iam.change.after_unknown.project != true
    and $repo_iam.change.after_unknown.location != true
    and $repo_iam.change.after_unknown.repository != true
    and $repo_iam.change.after_unknown.role != true
    and $repo_iam.change.after_unknown.member != true
    and $repo_iam_config.mode == "managed"
    and $repo_iam_config.type
      == "google_artifact_registry_repository_iam_member"
    and $repo_iam_config.expressions.project.references
      == ["var.gcp_project_id"]
    and $repo_iam_config.expressions.location.references
      == ["var.gcp_region"]
    and $repo_iam_config.expressions.repository.references
      == [
        "google_artifact_registry_repository.custom_environments_repository.repository_id",
        "google_artifact_registry_repository.custom_environments_repository"
      ]
    and $repo_iam_config.expressions.member.references
      == ["module.init.service_account_email", "module.init"]
    and $read_secret.change.after.project == $expected_project
    and $read_secret.change.after_unknown.project != true
    and $read_secret.change.after.secret_id
      == ($expected_prefix + "postgres-read-replica-connection-string")
    and ($read_secret.change.after.replication | length) == 1
    and ($read_secret.change.after.replication[0].auto | length) == 1
    and $read_secret.change.after.replication[0].auto[0].customer_managed_encryption == []
    and $read_secret.change.after.replication[0].user_managed == []
    and $read_version.change.after.secret == null
    and $read_version.change.after_unknown.secret == true
    and $read_version_config.expressions.secret.references
      == [
        "google_secret_manager_secret.postgres_read_replica_connection_string.name",
        "google_secret_manager_secret.postgres_read_replica_connection_string"
      ]
    and $connection_version_config.expressions.secret.references
      == ["module.init.postgres_connection_string_secret_name", "module.init"]
    and $read_version.change.after.secret_data == " "
    and $read_version.change.after_sensitive.secret_data == true
    and $seed_password.change.after.length == 32
    and $seed_password.change.after.special == false
    and $seed_password.change.after.result == null
    and $seed_password.change.after_unknown.result == true
    and $seed_password.change.after_sensitive.result == true
    and $seed_secret.change.after.project == $expected_project
    and $seed_secret.change.after_unknown.project != true
    and $seed_secret.change.after.secret_id
      == ($expected_prefix + "sandbox-access-token-hash-seed")
    and ($seed_secret.change.after.replication | length) == 1
    and ($seed_secret.change.after.replication[0].auto | length) == 1
    and $seed_secret.change.after.replication[0].auto[0].customer_managed_encryption == []
    and $seed_secret.change.after.replication[0].user_managed == []
    and $seed_version.change.after.secret == null
    and $seed_version.change.after_unknown.secret == true
    and $seed_version.change.after.secret_data == null
    and $seed_version.change.after_sensitive.secret_data == true
    and $seed_version.change.after_unknown.secret_data == true
    and $seed_version_config.expressions.secret.references
      == [
        "google_secret_manager_secret.sandbox_access_token_hash_seed.id",
        "google_secret_manager_secret.sandbox_access_token_hash_seed"
      ]
    and $seed_version_config.expressions.secret_data.references
      == [
        "random_password.sandbox_access_token_hash_seed.result",
        "random_password.sandbox_access_token_hash_seed"
      ]
    and $volume_key.change.after.algorithm == "ED25519"
    and $volume_key.change.after_sensitive.private_key_openssh == true
    and $volume_key.change.after_sensitive.private_key_pem == true
    and $volume_key.change.after_unknown.private_key_openssh == true
    and $volume_key.change.after_unknown.private_key_pem == true
    and $volume_time.change.after.rfc3339 == null
    and $volume_time.change.after_unknown.rfc3339 == true
    and $volume_time.change.after.unix == null
    and $volume_time.change.after_unknown.unix == true
    and $sql_instance.change.after.project == $expected_project
    and $sql_instance.change.after.region == $expected_region
' <<<"${plan_json}" >/dev/null; then
  printf 'Refusing workload prerequisite plan: non-Cloud-SQL prerequisite identity or sensitive-value handling drifted.\n' >&2
  jq -c '
    def row($address):
      [.resource_changes[]? | select(.address == $address)][0];
    def config($address):
      [.configuration.root_module.resources[]? | select(.address == $address)][0];
    {
      repository: {
        after: (
          row("google_artifact_registry_repository.custom_environments_repository")
          | .change.after
          | {format, location, project, repository_id}
        ),
        after_unknown: (
          row("google_artifact_registry_repository.custom_environments_repository")
          | .change.after_unknown
          | {location, project, repository_id}
        ),
        configuration: (
          config("google_artifact_registry_repository.custom_environments_repository")
          | {provider_config_key, expressions}
        )
      },
      repository_iam: {
        after: (
          row("google_artifact_registry_repository_iam_member.custom_environments_repository_member")
          | .change.after
          | {location, member, project, repository, role}
        ),
        after_unknown: (
          row("google_artifact_registry_repository_iam_member.custom_environments_repository_member")
          | .change.after_unknown
          | {location, member, project, repository, role}
        ),
        configuration: (
          config("google_artifact_registry_repository_iam_member.custom_environments_repository_member")
          | {provider_config_key, expressions}
        )
      },
      connection_secret_reference: (
        config("google_secret_manager_secret_version.postgres_connection_string")
        | .expressions.secret.references
      ),
      read_secret: {
        project: (
          row("google_secret_manager_secret.postgres_read_replica_connection_string")
          | .change.after.project
        ),
        secret_reference: (
          config("google_secret_manager_secret_version.postgres_read_replica_connection_string")
          | .expressions.secret.references
        )
      },
      seed_secret: {
        project: (
          row("google_secret_manager_secret.sandbox_access_token_hash_seed")
          | .change.after.project
        ),
        secret_reference: (
          config("google_secret_manager_secret_version.sandbox_access_token_hash_seed")
          | .expressions.secret.references
        ),
        data_reference: (
          config("google_secret_manager_secret_version.sandbox_access_token_hash_seed")
          | .expressions.secret_data.references
        )
      },
      volume_time: (
        row("time_static.volume_token_generation")
        | {
            after: (.change.after | {rfc3339, unix}),
            after_unknown: (.change.after_unknown | {rfc3339, unix})
          }
      )
    }
  ' <<<"${plan_json}" >&2
  exit 1
fi

printf 'Workload prerequisite plan passed: exactly 23 reviewed creates, zero data reads, and zero Nomad resources.\n'
