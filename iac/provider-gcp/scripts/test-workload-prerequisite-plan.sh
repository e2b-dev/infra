#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
assertion_script="${script_dir}/assert-workload-prerequisite-plan.sh"
policy="${script_dir}/../topology/minimal-workload-policy.json"
makefile="${script_dir}/../Makefile"
cloud_sql_fixture="${script_dir}/testdata/cloud-sql-workload-resources.json"
cloud_sql_project_state="${script_dir}/testdata/cloud-sql-project-state.json"
test_dir="$(mktemp -d)"
trap 'rm -rf -- "${test_dir}"' EXIT

prerequisite_targets="$(
  awk '
    /^override WORKLOAD_PREREQUISITE_TARGETS :=/ { capture = 1 }
    capture { print }
    capture && $0 !~ /\\[[:space:]]*$/ { exit }
  ' "${makefile}"
)"
for candidate_target in \
  google_sql_database_instance.invited_beta \
  google_sql_database.invited_beta \
  random_password.cloud_sql_invited_beta \
  google_sql_user.invited_beta \
  google_secret_manager_secret_version.cloud_sql_invited_beta_password; do
  grep -F -- "-target='${candidate_target}'" <<<"${prerequisite_targets}" >/dev/null || {
    printf 'Missing invited-beta Cloud SQL prerequisite target: %s\n' \
      "${candidate_target}" >&2
    exit 1
  }
done

fake_terraform="${test_dir}/terraform"
cp "${script_dir}/testdata/fake-terraform.sh" "${fake_terraform}"
chmod 0700 "${fake_terraform}"

jq -n \
  --slurpfile cloud_sql "${cloud_sql_fixture}" \
  --slurpfile cloud_sql_project "${cloud_sql_project_state}" '
  {
    format_version: "1.2",
    terraform_version: "1.7.5",
    resource_changes: (
      (
        $cloud_sql[0]
        | map(
            if .provider_name != null then .
            elif (.type | startswith("google_")) then
              .provider_name = "registry.terraform.io/hashicorp/google"
            elif (.type | startswith("random_")) then
              .provider_name = "registry.terraform.io/hashicorp/random"
            elif .type == "terraform_data" then
              .provider_name = "terraform.io/builtin/terraform"
            else .
            end
          )
      )
      + [
        {
          address: "google_artifact_registry_repository.custom_environments_repository",
          mode: "managed",
          type: "google_artifact_registry_repository",
          name: "custom_environments_repository",
          provider_name: "registry.terraform.io/hashicorp/google",
          change: {
            actions: ["create"],
            before: null,
            after: {
              format: "DOCKER",
              location: "us-east4",
              project: "operator-canary",
              repository_id: "e2b-custom-environments"
            },
            after_unknown: {name: true}
          }
        },
        {
          address: "google_artifact_registry_repository_iam_member.custom_environments_repository_member",
          mode: "managed",
          type: "google_artifact_registry_repository_iam_member",
          name: "custom_environments_repository_member",
          provider_name: "registry.terraform.io/hashicorp/google",
          change: {
            actions: ["create"],
            before: null,
            after: {
              member: "serviceAccount:e2b-infra-instances@operator-canary.iam.gserviceaccount.com",
              location: "us-east4",
              project: "operator-canary",
              repository: "e2b-custom-environments",
              role: "roles/artifactregistry.repoAdmin"
            },
            after_unknown: {}
          }
        },
        {
          address: "google_secret_manager_secret.postgres_read_replica_connection_string",
          mode: "managed",
          type: "google_secret_manager_secret",
          name: "postgres_read_replica_connection_string",
          provider_name: "registry.terraform.io/hashicorp/google",
          change: {
            actions: ["create"],
            before: null,
            after: {
              project: "operator-canary",
              replication: [{
                auto: [{customer_managed_encryption: []}],
                user_managed: []
              }],
              secret_id: "e2b-postgres-read-replica-connection-string"
            },
            after_unknown: {name: true}
          }
        },
        {
          address: "google_secret_manager_secret_version.postgres_read_replica_connection_string",
          mode: "managed",
          type: "google_secret_manager_secret_version",
          name: "postgres_read_replica_connection_string",
          provider_name: "registry.terraform.io/hashicorp/google",
          change: {
            actions: ["create"],
            before: null,
            after: {secret: null, secret_data: " "},
            after_unknown: {secret: true, version: true},
            after_sensitive: {secret_data: true}
          }
        },
        {
          address: "random_password.sandbox_access_token_hash_seed",
          mode: "managed",
          type: "random_password",
          name: "sandbox_access_token_hash_seed",
          provider_name: "registry.terraform.io/hashicorp/random",
          change: {
            actions: ["create"],
            before: null,
            after: {length: 32, special: false},
            after_unknown: {result: true},
            after_sensitive: {result: true}
          }
        },
        {
          address: "google_secret_manager_secret.sandbox_access_token_hash_seed",
          mode: "managed",
          type: "google_secret_manager_secret",
          name: "sandbox_access_token_hash_seed",
          provider_name: "registry.terraform.io/hashicorp/google",
          change: {
            actions: ["create"],
            before: null,
            after: {
              project: "operator-canary",
              replication: [{
                auto: [{customer_managed_encryption: []}],
                user_managed: []
              }],
              secret_id: "e2b-sandbox-access-token-hash-seed"
            },
            after_unknown: {name: true}
          }
        },
        {
          address: "google_secret_manager_secret_version.sandbox_access_token_hash_seed",
          mode: "managed",
          type: "google_secret_manager_secret_version",
          name: "sandbox_access_token_hash_seed",
          provider_name: "registry.terraform.io/hashicorp/google",
          change: {
            actions: ["create"],
            before: null,
            after: {secret: null, secret_data: null},
            after_unknown: {secret: true, secret_data: true, version: true},
            after_sensitive: {secret_data: true}
          }
        },
        {
          address: "time_static.volume_token_generation",
          mode: "managed",
          type: "time_static",
          name: "volume_token_generation",
          provider_name: "registry.terraform.io/hashicorp/time",
          change: {
            actions: ["create"],
            before: null,
            after: {rfc3339: null, unix: null},
            after_unknown: {rfc3339: true, unix: true}
          }
        },
        {
          address: "tls_private_key.volume_token[0]",
          mode: "managed",
          type: "tls_private_key",
          name: "volume_token",
          index: 0,
          provider_name: "registry.terraform.io/hashicorp/tls",
          change: {
            actions: ["create"],
            before: null,
            after: {algorithm: "ED25519"},
            after_unknown: {
              private_key_openssh: true,
              private_key_pem: true
            },
            after_sensitive: {
              private_key_openssh: true,
              private_key_pem: true
            }
          }
        }
      ]
    ),
    prior_state: {
      format_version: "1.0",
      values: {
        root_module: {
          child_modules: [{
            address: "module.init",
            resources: [$cloud_sql_project[0]]
          }]
        }
      }
    },
    configuration: {
      provider_config: {
        google: {
          name: "google",
          full_name: "registry.terraform.io/hashicorp/google",
          expressions: {
            project: {references: ["var.gcp_project_id"]},
            region: {references: ["var.gcp_region"]}
          }
        }
      },
      root_module: {
        resources: [
          {
            address: "google_artifact_registry_repository.custom_environments_repository",
            mode: "managed",
            type: "google_artifact_registry_repository",
            name: "custom_environments_repository",
            provider_config_key: "google",
            expressions: {
              location: {references: ["var.gcp_region"]},
              project: {references: ["var.gcp_project_id"]}
            }
          },
          {
            address: "google_artifact_registry_repository_iam_member.custom_environments_repository_member",
            mode: "managed",
            type: "google_artifact_registry_repository_iam_member",
            name: "custom_environments_repository_member",
            provider_config_key: "google",
            expressions: {
              location: {references: ["var.gcp_region"]},
              member: {
                references: ["module.init.service_account_email", "module.init"]
              },
              project: {references: ["var.gcp_project_id"]},
              repository: {
                references: [
                  "google_artifact_registry_repository.custom_environments_repository.repository_id",
                  "google_artifact_registry_repository.custom_environments_repository"
                ]
              }
            }
          },
          {
            address: "google_secret_manager_secret_version.postgres_connection_string",
            mode: "managed",
            type: "google_secret_manager_secret_version",
            name: "postgres_connection_string",
            provider_config_key: "google",
            expressions: {
              secret: {
                references: [
                  "module.init.postgres_connection_string_secret_name",
                  "module.init"
                ]
              }
            }
          },
          {
            address: "google_secret_manager_secret_version.postgres_read_replica_connection_string",
            mode: "managed",
            type: "google_secret_manager_secret_version",
            name: "postgres_read_replica_connection_string",
            provider_config_key: "google",
            expressions: {
              secret: {
                references: [
                  "google_secret_manager_secret.postgres_read_replica_connection_string.name",
                  "google_secret_manager_secret.postgres_read_replica_connection_string"
                ]
              }
            }
          },
          {
            address: "google_secret_manager_secret_version.sandbox_access_token_hash_seed",
            mode: "managed",
            type: "google_secret_manager_secret_version",
            name: "sandbox_access_token_hash_seed",
            provider_config_key: "google",
            expressions: {
              secret: {
                references: [
                  "google_secret_manager_secret.sandbox_access_token_hash_seed.id",
                  "google_secret_manager_secret.sandbox_access_token_hash_seed"
                ]
              },
              secret_data: {
                references: [
                  "random_password.sandbox_access_token_hash_seed.result",
                  "random_password.sandbox_access_token_hash_seed"
                ]
              }
            }
          },
          {
            address: "google_secret_manager_secret_version.cloud_sql_invited_beta_password",
            mode: "managed",
            type: "google_secret_manager_secret_version",
            name: "cloud_sql_invited_beta_password",
            provider_config_key: "google",
            expressions: {
              secret: {
                references: [
                  "google_secret_manager_secret.cloud_sql_invited_beta_password.name",
                  "google_secret_manager_secret.cloud_sql_invited_beta_password"
                ]
              },
              secret_data: {
                references: [
                  "random_password.cloud_sql_invited_beta.result",
                  "random_password.cloud_sql_invited_beta"
                ]
              }
            }
          }
        ]
      }
    }
  }
  ' >"${test_dir}/reviewed.json"

run_assertion() {
  local fixture="$1"
  local policy_path="${2:-${policy}}"

  WORKLOAD_GCP_PROJECT_ID=operator-canary \
  WORKLOAD_GCP_REGION=us-east4 \
  WORKLOAD_PREFIX=e2b- \
    "${assertion_script}" "${fixture}" "${fake_terraform}" "${policy_path}"
}

expect_failure() {
  local name="$1"
  local expected_message="$2"
  local fixture="$3"
  local policy_path="${4:-${policy}}"

  if run_assertion "${fixture}" "${policy_path}" >"${test_dir}/${name}.out" 2>&1; then
    printf 'Expected prerequisite fixture %s to fail.\n' "${name}" >&2
    exit 1
  fi
  grep -F "${expected_message}" "${test_dir}/${name}.out" >/dev/null || {
    printf 'Prerequisite fixture %s failed for an unexpected reason.\n' "${name}" >&2
    sed -n '1,120p' "${test_dir}/${name}.out" >&2
    exit 1
  }
}

run_assertion "${test_dir}/reviewed.json" >/dev/null

jq '
  (
    .resource_changes[]
    | select(.address == "terraform_data.cloud_sql_connection_budget")
    | .change
  ) |= (
    .actions = ["update"]
    | .before = {
        input: (
          .after.input
          + {
              api_server_count: 1,
              maximum_concurrent_connections: 19
            }
        )
      }
  )
' "${test_dir}/reviewed.json" >"${test_dir}/bounded-budget-update.json"
run_assertion "${test_dir}/bounded-budget-update.json" >/dev/null

jq '
  (
    .resource_changes[]
    | select(.address == "terraform_data.cloud_sql_connection_budget")
    | .change.before.input.api_server_count
  ) = 3
' "${test_dir}/bounded-budget-update.json" \
  >"${test_dir}/unbounded-budget-update.json"
expect_failure \
  unbounded-budget-update \
  "connection-budget update must be exactly" \
  "${test_dir}/unbounded-budget-update.json"

jq '
  (
    .resource_changes[]
    | select(.address == "time_sleep.service_identity_propagation")
    | .change
  ) |= (
    .actions = ["no-op"]
    | .before = .after
    | .after_unknown = {}
  )
' "${test_dir}/reviewed.json" >"${test_dir}/recovery.json"
run_assertion "${test_dir}/recovery.json" >/dev/null

jq '
  .resource_changes |= map(
    if (
      .address
      | IN(
          "google_artifact_registry_repository.custom_environments_repository",
          "google_artifact_registry_repository_iam_member.custom_environments_repository_member",
          "google_secret_manager_secret.postgres_read_replica_connection_string",
          "google_secret_manager_secret_version.postgres_read_replica_connection_string",
          "random_password.sandbox_access_token_hash_seed",
          "google_secret_manager_secret.sandbox_access_token_hash_seed",
          "google_secret_manager_secret_version.sandbox_access_token_hash_seed",
          "time_static.volume_token_generation",
          "tls_private_key.volume_token[0]"
        )
    ) then
      .change.actions = ["no-op"]
      | .change.before = .change.after
      | .change.after_unknown = {}
    else
      .
    end
  )
  | (
      .resource_changes[]
      | select(
          .address
          == "google_artifact_registry_repository_iam_member.custom_environments_repository_member"
        )
      | .change.before.repository,
        .change.after.repository
    ) = "projects/operator-canary/locations/us-east4/repositories/e2b-custom-environments"
  | (
      .resource_changes[]
      | select(.address == "time_static.volume_token_generation")
      | .change.before,
        .change.after
    ) = {
      rfc3339: "2026-07-29T11:09:40Z",
      unix: 1785323380
    }
' "${test_dir}/reviewed.json" >"${test_dir}/partial-live-state.json"
run_assertion "${test_dir}/partial-live-state.json" >/dev/null

jq '
  (
    .resource_changes[]
    | select(
        .address
        == "google_compute_global_address.cloud_sql_private_services"
      )
    | .change.after.network
  ) = "https://www.googleapis.com/compute/v1/projects/operator-canary/global/networks/default"
' "${test_dir}/reviewed.json" >"${test_dir}/network-self-link.json"
run_assertion "${test_dir}/network-self-link.json" >/dev/null

jq '
  .resource_changes |= map(
    select(
      .address
      != "google_secret_manager_secret_version.sandbox_access_token_hash_seed"
    )
  )
' "${test_dir}/reviewed.json" >"${test_dir}/missing.json"
expect_failure \
  missing \
  "resource set must be the exact reviewed resources" \
  "${test_dir}/missing.json"

jq '
  .resource_changes += [{
    address: "google_storage_bucket.unreviewed",
    mode: "managed",
    type: "google_storage_bucket",
    name: "unreviewed",
    change: {
      actions: ["create"],
      before: null,
      after: {name: "unreviewed"},
      after_unknown: {}
    }
  }]
' "${test_dir}/reviewed.json" >"${test_dir}/extra.json"
expect_failure \
  extra \
  "resource set must be the exact reviewed resources" \
  "${test_dir}/extra.json"

jq '
  (
    .resource_changes[]
    | select(.address == "time_static.volume_token_generation")
    | .change.actions
  ) = ["update"]
' "${test_dir}/reviewed.json" >"${test_dir}/update.json"
expect_failure \
  update \
  "resource set must be the exact reviewed resources" \
  "${test_dir}/update.json"

jq '
  .resource_changes |= map(
    if (
      .mode == "managed"
      and .change.actions == ["create"]
    ) then
      .change.actions = ["no-op"]
      | .change.before = .change.after
      | .change.after_unknown = {}
    else
      .
    end
  )
' "${test_dir}/reviewed.json" >"${test_dir}/complete.json"
expect_failure \
  complete \
  "including at least one create" \
  "${test_dir}/complete.json"

jq '
  .resource_changes += [{
    address: "data.google_secret_manager_secret_version.deferred",
    mode: "data",
    type: "google_secret_manager_secret_version",
    name: "deferred",
    change: {
      actions: ["read"],
      before: null,
      after: {},
      after_unknown: {secret_data: true}
    }
  }]
' "${test_dir}/reviewed.json" >"${test_dir}/deferred-read.json"
expect_failure \
  deferred-read \
  "deferred data reads must be empty" \
  "${test_dir}/deferred-read.json"

jq '
  .resource_changes += [{
    address: "module.nomad.nomad_job.unreviewed",
    module_address: "module.nomad",
    mode: "managed",
    type: "nomad_job",
    name: "unreviewed",
    change: {
      actions: ["no-op"],
      before: {},
      after: {},
      after_unknown: {}
    }
  }]
' "${test_dir}/reviewed.json" >"${test_dir}/nomad-noop.json"
expect_failure \
  nomad-noop \
  "Nomad resources must be absent" \
  "${test_dir}/nomad-noop.json"

jq '
  (
    .resource_changes[]
    | select(
        .address
        == "google_artifact_registry_repository.custom_environments_repository"
      )
    | .change.after.project
  ) = "attacker-project"
' "${test_dir}/reviewed.json" >"${test_dir}/wrong-project.json"
expect_failure \
  wrong-project \
  "non-Cloud-SQL prerequisite identity" \
  "${test_dir}/wrong-project.json"

jq '
  .configuration.provider_config.google.expressions.project.references
    = ["var.attacker_project"]
' "${test_dir}/reviewed.json" >"${test_dir}/wrong-provider-wiring.json"
expect_failure \
  wrong-provider-wiring \
  "non-Cloud-SQL prerequisite identity" \
  "${test_dir}/wrong-provider-wiring.json"

jq '
  (
    .resource_changes[]
    | select(
        .address
        == "google_artifact_registry_repository.custom_environments_repository"
      )
    | .change.after.location
  ) = "us-west1"
' "${test_dir}/reviewed.json" >"${test_dir}/wrong-repository-location.json"
expect_failure \
  wrong-repository-location \
  "non-Cloud-SQL prerequisite identity" \
  "${test_dir}/wrong-repository-location.json"

jq '
  (
    .resource_changes[]
    | select(
        .address
        == "google_artifact_registry_repository_iam_member.custom_environments_repository_member"
      )
    | .change.after.member
  ) = "serviceAccount:attacker@operator-canary.iam.gserviceaccount.com"
' "${test_dir}/reviewed.json" >"${test_dir}/wrong-repository-member.json"
expect_failure \
  wrong-repository-member \
  "non-Cloud-SQL prerequisite identity" \
  "${test_dir}/wrong-repository-member.json"

jq '
  (
    .configuration.root_module.resources[]
    | select(
        .address
        == "google_artifact_registry_repository_iam_member.custom_environments_repository_member"
      )
    | .expressions.repository.references
  ) = ["google_artifact_registry_repository.attacker.name"]
' "${test_dir}/reviewed.json" >"${test_dir}/wrong-repository-binding.json"
expect_failure \
  wrong-repository-binding \
  "non-Cloud-SQL prerequisite identity" \
  "${test_dir}/wrong-repository-binding.json"

jq '
  (
    .resource_changes[]
    | select(
        .address
        == "google_secret_manager_secret.postgres_read_replica_connection_string"
      )
    | .change.after.project
  ) = "attacker-project"
' "${test_dir}/reviewed.json" >"${test_dir}/wrong-read-secret-project.json"
expect_failure \
  wrong-read-secret-project \
  "non-Cloud-SQL prerequisite identity" \
  "${test_dir}/wrong-read-secret-project.json"

jq '
  (
    .configuration.root_module.resources[]
    | select(
        .address
        == "google_secret_manager_secret_version.postgres_read_replica_connection_string"
      )
    | .expressions.secret.references
  ) = ["google_secret_manager_secret.attacker.name"]
' "${test_dir}/reviewed.json" >"${test_dir}/wrong-read-secret-binding.json"
expect_failure \
  wrong-read-secret-binding \
  "non-Cloud-SQL prerequisite identity" \
  "${test_dir}/wrong-read-secret-binding.json"

jq '
  (
    .configuration.root_module.resources[]
    | select(
        .address
        == "google_secret_manager_secret_version.postgres_connection_string"
      )
    | .expressions.secret.references
  ) = ["module.attacker.secret_name"]
' "${test_dir}/reviewed.json" >"${test_dir}/wrong-connection-secret-binding.json"
expect_failure \
  wrong-connection-secret-binding \
  "non-Cloud-SQL prerequisite identity" \
  "${test_dir}/wrong-connection-secret-binding.json"

jq '
  (
    .resource_changes[]
    | select(
        .address
        == "google_secret_manager_secret_version.sandbox_access_token_hash_seed"
      )
    | .change.after_sensitive.secret_data
  ) = false
' "${test_dir}/reviewed.json" >"${test_dir}/unprotected-seed.json"
expect_failure \
  unprotected-seed \
  "non-Cloud-SQL prerequisite identity" \
  "${test_dir}/unprotected-seed.json"

jq '
  (
    .resource_changes[]
    | select(
        .address
        == "google_secret_manager_secret.sandbox_access_token_hash_seed"
      )
    | .change.after.project
  ) = "attacker-project"
' "${test_dir}/reviewed.json" >"${test_dir}/wrong-seed-secret-project.json"
expect_failure \
  wrong-seed-secret-project \
  "non-Cloud-SQL prerequisite identity" \
  "${test_dir}/wrong-seed-secret-project.json"

jq '
  (
    .configuration.root_module.resources[]
    | select(
        .address
        == "google_secret_manager_secret_version.sandbox_access_token_hash_seed"
      )
    | .expressions.secret.references
  ) = ["google_secret_manager_secret.attacker.id"]
' "${test_dir}/reviewed.json" >"${test_dir}/wrong-seed-secret-binding.json"
expect_failure \
  wrong-seed-secret-binding \
  "non-Cloud-SQL prerequisite identity" \
  "${test_dir}/wrong-seed-secret-binding.json"

jq '
  (
    .configuration.root_module.resources[]
    | select(
        .address
        == "google_secret_manager_secret_version.sandbox_access_token_hash_seed"
      )
    | .expressions.secret_data.references
  ) = ["random_password.attacker.result"]
' "${test_dir}/reviewed.json" >"${test_dir}/wrong-seed-data-binding.json"
expect_failure \
  wrong-seed-data-binding \
  "non-Cloud-SQL prerequisite identity" \
  "${test_dir}/wrong-seed-data-binding.json"

jq '
  (
    .resource_changes[]
    | select(.address == "time_static.volume_token_generation")
    | .change.after_unknown.unix
  ) = false
' "${test_dir}/reviewed.json" >"${test_dir}/known-volume-time-unix.json"
expect_failure \
  known-volume-time-unix \
  "non-Cloud-SQL prerequisite identity" \
  "${test_dir}/known-volume-time-unix.json"

jq '
  (
    .resource_changes[]
    | select(.address == "google_sql_database_instance.operator_canary")
    | .change.after.region
  ) = "us-west1"
' "${test_dir}/reviewed.json" >"${test_dir}/wrong-sql-region.json"
expect_failure \
  wrong-sql-region \
  "invalid_cloud_sql_resources must be empty" \
  "${test_dir}/wrong-sql-region.json"

jq '
  (
    .resource_changes[]
    | select(.address == "google_sql_database_instance.invited_beta")
    | .change.after.settings[0].tier
  ) = "db-f1-micro"
' "${test_dir}/reviewed.json" >"${test_dir}/wrong-candidate-sql-tier.json"
expect_failure \
  wrong-candidate-sql-tier \
  "invalid_cloud_sql_resources must be empty" \
  "${test_dir}/wrong-candidate-sql-tier.json"

jq '
  (
    .resource_changes[]
    | select(
        .address
        == "google_secret_manager_secret.cloud_sql_invited_beta_password"
      )
    | .change.after.secret_id
  ) = "e2b-wrong-password-secret"
' "${test_dir}/reviewed.json" \
  >"${test_dir}/wrong-candidate-password-secret.json"
expect_failure \
  wrong-candidate-password-secret \
  "invalid_cloud_sql_resources must be empty" \
  "${test_dir}/wrong-candidate-password-secret.json"

jq '
  .expected_cloud_sql.application_connection_budget = 99
' "${policy}" >"${test_dir}/loose-cloud-sql-policy.json"
expect_failure \
  loose-cloud-sql-policy \
  "Cloud SQL policy must match the exact reviewed contract" \
  "${test_dir}/reviewed.json" \
  "${test_dir}/loose-cloud-sql-policy.json"

printf 'Workload prerequisite plan assertions passed.\n'
