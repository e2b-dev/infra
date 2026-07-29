#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
assertion_script="${script_dir}/assert-workload-plan.sh"
packer_assertion_script="${script_dir}/assert-packer-reserve.sh"
fixture="${script_dir}/testdata/minimal-workload-plan.json"
cloud_sql_fixture="${script_dir}/testdata/cloud-sql-workload-resources.json"
artifact_bindings_fixture="${script_dir}/testdata/workload-artifact-plan-bindings.json"
artifacts="${script_dir}/testdata/workload-artifacts.json"
policy="${script_dir}/../topology/minimal-workload-policy.json"
packer_template="${script_dir}/../nomad-cluster-disk-image/main.pkr.hcl"
cloud_sql_config="${script_dir}/../cloud-sql.tf"
reverse_proxy_store="${script_dir}/../../../packages/docker-reverse-proxy/internal/handlers/store.go"
dashboard_api_main="${script_dir}/../../../packages/dashboard-api/main.go"
database_migrator="${script_dir}/../../../packages/db/scripts/migrator.go"
test_dir="$(mktemp -d)"
trap 'rm -rf "${test_dir}"' EXIT

fake_terraform="${test_dir}/terraform"
cp "${script_dir}/testdata/fake-terraform.sh" "${fake_terraform}"
chmod 0700 "${fake_terraform}"

jq \
  --slurpfile cloud_sql "${cloud_sql_fixture}" \
  --slurpfile artifact_bindings "${artifact_bindings_fixture}" '
  .resource_changes += (
    $cloud_sql[0]
    + $artifact_bindings[0].resource_changes
  )
  | .planned_values = $artifact_bindings[0].planned_values
  | $artifact_bindings[0].orchestrator_source_image as $source_image
  | (
      .resource_changes[]
      | select(.type == "google_compute_instance_template")
      | .change.after.disk[]
      | select(.boot == true)
      | .source_image
    ) = $source_image
  ' \
  "${fixture}" >"${test_dir}/minimal-with-cloud-sql.json"
bootstrap_fixture="${test_dir}/minimal-with-cloud-sql.json"
jq '
  .resource_changes |= map(
    if (
      .mode == "managed"
      and (.address | startswith("module.cluster."))
      and (.type | startswith("google_compute_"))
    )
    then (
      .change.before = .change.after
      | .change.actions = ["no-op"]
    )
    else .
    end
  )
' "${bootstrap_fixture}" >"${test_dir}/phase-two-minimal.json"
fixture="${test_dir}/phase-two-minimal.json"

grep -F 'ssl_mode                                      = "ENCRYPTED_ONLY"' \
  "${cloud_sql_config}" >/dev/null
grep -F '"postgresql://%s:%s@%s:5432/%s?sslmode=require"' \
  "${cloud_sql_config}" >/dev/null
grep -F 'password = random_password.cloud_sql_operator_canary.result' \
  "${cloud_sql_config}" >/dev/null
grep -F 'secret = module.init.postgres_connection_string_secret_name' \
  "${cloud_sql_config}" >/dev/null
grep -F 'member  = "serviceAccount:${google_project_service_identity.cloud_sql.email}"' \
  "${cloud_sql_config}" >/dev/null
grep -F 'member  = "serviceAccount:${google_project_service_identity.service_networking.email}"' \
  "${cloud_sql_config}" >/dev/null
grep -F 'prevent_destroy = true' \
  "${cloud_sql_config}" >/dev/null
test "$(grep -Fc 'pool.WithMaxConnections(3)' "${reverse_proxy_store}")" -eq 2
test "$(grep -Fc 'pool.WithMaxConnections(8)' "${dashboard_api_main}")" -eq 2
grep -F 'poolConfig.MaxConns = 4' "${database_migrator}" >/dev/null

expect_failure() {
  local name="$1"
  local expected_message="$2"
  local plan_path="$3"
  local policy_path="${4:-${policy}}"
  local packer_template_path="${5:-${packer_template}}"
  local artifacts_path="${6:-${artifacts}}"
  local scope="${7:-full}"
  local output_path="${test_dir}/${name}.output"

  if "${assertion_script}" \
    "${plan_path}" \
    "${fake_terraform}" \
    "${policy_path}" \
    "${packer_template_path}" \
    "${artifacts_path}" \
    "${scope}" >"${output_path}" 2>&1; then
    printf 'Expected %s fixture to fail.\n' "${name}" >&2
    exit 1
  fi

  grep -F "${expected_message}" "${output_path}" >/dev/null || {
    printf 'Fixture %s failed for an unexpected reason:\n' "${name}" >&2
    sed -n '1,160p' "${output_path}" >&2
    exit 1
  }
}

expect_success() {
  local name="$1"
  local plan_path="$2"
  local scope="${3:-full}"
  local output_path="${test_dir}/${name}.output"

  "${assertion_script}" \
    "${plan_path}" \
    "${fake_terraform}" \
    "${policy}" \
    "${packer_template}" \
    "${artifacts}" \
    "${scope}" >"${output_path}"

  grep -F '"global_vcpus":30' "${output_path}" >/dev/null
  grep -F '"regional_cpus":30' "${output_path}" >/dev/null
  grep -F '"instances":7' "${output_path}" >/dev/null
  grep -F '"pd_ssd_gb":270' "${output_path}" >/dev/null
  grep -F '"pd_standard_gb":400' "${output_path}" >/dev/null
  grep -F '"local_ssd_gb":750' "${output_path}" >/dev/null
  grep -F '"regional_public_ips":7' "${output_path}" >/dev/null
  if [[ "${scope}" == "cluster" ]]; then
    grep -F \
      'Cluster bootstrap scope passed: only module.cluster may mutate' \
      "${output_path}" >/dev/null
  fi
}

expect_success "minimal" "${fixture}"

jq '
  (
    .resource_changes[]
    | select(.address == "module.cluster.google_compute_instance_template.api")
    | .change.after.disk[0]
  ) |= del(.type)
  | (
      .resource_changes[]
      | select(.address == "module.cluster.google_compute_instance_template.api")
      | .change.after_unknown.disk
    ) = [{"type": true}]
' "${fixture}" >"${test_dir}/computed-persistent-disk-type.json"
expect_success \
  "computed-persistent-disk-type" \
  "${test_dir}/computed-persistent-disk-type.json"

jq '
  (
    .planned_values.root_module
    | recurse(.child_modules[]?)
    | .resources?
  ) |= map(select(.type != "google_compute_image"))
' "${fixture}" >"${test_dir}/resolved-image-data-sources-omitted.json"
expect_success \
  "resolved-image-data-sources-omitted" \
  "${test_dir}/resolved-image-data-sources-omitted.json"

jq '
  (
    .planned_values.root_module
    | recurse(.child_modules[]?)
    | .resources[]?
    | select(.type == "google_compute_image")
    | .values
  ) = {
    family: "e2b-orch",
    filter: null,
    most_recent: null,
    project: "monad-code"
  }
' "${fixture}" >"${test_dir}/deferred-image-data-sources.json"
expect_success \
  "deferred-image-data-sources" \
  "${test_dir}/deferred-image-data-sources.json"

jq '
  (
    .planned_values.root_module
    | recurse(.child_modules[]?)
    | .resources[]?
    | select(.type == "google_compute_image")
    | .values
  ) = {
    family: "e2b-orch",
    filter: null,
    most_recent: null,
    project: "monad-code"
  }
  | (
      .resource_changes[]
      | select(
          .address
          == "module.cluster.module.build_cluster[\"default\"].google_compute_instance_template.template"
          or .address
          == "module.cluster.module.client_cluster[\"default\"].google_compute_instance_template.template"
        )
      | .change.after.disk[]
      | select(.boot == true)
      | .source_image
    ) = null
  | (
      .resource_changes[]
      | select(
          .address
          == "module.cluster.module.build_cluster[\"default\"].google_compute_instance_template.template"
          or .address
          == "module.cluster.module.client_cluster[\"default\"].google_compute_instance_template.template"
        )
      | .change.after_unknown.disk
    ) = [{"source_image": true}, {}]
' "${fixture}" >"${test_dir}/deferred-worker-template-images.json"
expect_success \
  "deferred-worker-template-images" \
  "${test_dir}/deferred-worker-template-images.json"

jq '
  (
    .resource_changes[]
    | select(
        .address
        == "module.cluster.module.build_cluster[\"default\"].google_compute_instance_template.template"
      )
    | .change.after_unknown.disk
  ) = [{}, {}]
' "${test_dir}/deferred-worker-template-images.json" \
  >"${test_dir}/unbound-deferred-worker-template-image.json"
expect_failure \
  "unbound-deferred-worker-template-image" \
  "invalid_template_source_images must be empty." \
  "${test_dir}/unbound-deferred-worker-template-image.json"

jq '
  (
    .planned_values.root_module
    | recurse(.child_modules[]?)
    | .resources[]?
    | select(
        .address
        == "module.cluster.module.build_cluster[\"default\"].data.google_compute_image.source_image"
      )
    | .values.family
  ) = "unreviewed"
' "${test_dir}/deferred-worker-template-images.json" \
  >"${test_dir}/unreviewed-deferred-worker-image-family.json"
expect_failure \
  "unreviewed-deferred-worker-image-family" \
  "invalid_orchestrator_images must be empty." \
  "${test_dir}/unreviewed-deferred-worker-image-family.json"

jq '
  (
    .planned_values.root_module
    | recurse(.child_modules[]?)
    | .resources[]?
    | select(
        .address
        == "module.cluster.module.build_cluster[\"default\"].data.google_compute_image.source_image"
      )
    | .values.project
  ) = "attacker-project"
' "${test_dir}/deferred-worker-template-images.json" \
  >"${test_dir}/unreviewed-deferred-worker-image-project.json"
expect_failure \
  "unreviewed-deferred-worker-image-project" \
  "invalid_orchestrator_images must be empty." \
  "${test_dir}/unreviewed-deferred-worker-image-project.json"

jq '
  (
    .resource_changes[]
    | select(.address == "module.cluster.google_compute_instance_template.api")
    | .change.actions
  ) = ["update"]
' "${fixture}" >"${test_dir}/phase-two-cluster-compute-update.json"
expect_failure \
  "phase-two-cluster-compute-update" \
  "module.cluster compute mutations must be empty." \
  "${test_dir}/phase-two-cluster-compute-update.json"

jq '
  .resource_changes += [{
    "address": "module.cluster.google_compute_disk.unreviewed",
    "mode": "managed",
    "type": "google_compute_disk",
    "name": "unreviewed",
    "change": {
      "actions": ["create"],
      "before": null,
      "after": {"name": "unreviewed"}
    }
  }]
' "${fixture}" >"${test_dir}/phase-two-cluster-compute-create.json"
expect_failure \
  "phase-two-cluster-compute-create" \
  "module.cluster compute mutations must be empty." \
  "${test_dir}/phase-two-cluster-compute-create.json"

jq '
  .resource_changes |= map(
    select(.address | startswith("module.cluster."))
  )
  | (
      .planned_values.root_module
      | recurse(.child_modules[]?)
      | .resources?
    ) |= map(
      select(.address | startswith("module.cluster."))
  )
' "${bootstrap_fixture}" >"${test_dir}/cluster-minimal.json"
cluster_fixture="${test_dir}/cluster-minimal.json"
expect_success "cluster-minimal" "${cluster_fixture}" cluster

jq '
  .resource_changes += [{
    "address": "google_storage_bucket.unrelated",
    "mode": "managed",
    "type": "google_storage_bucket",
    "name": "unrelated",
    "change": {
      "actions": ["create"],
      "before": null,
      "after": {"name": "unrelated"}
    }
  }]
' "${cluster_fixture}" >"${test_dir}/cluster-outside-mutation.json"
expect_failure \
  "cluster-outside-mutation" \
  "mutations outside module.cluster must be empty." \
  "${test_dir}/cluster-outside-mutation.json" \
  "${policy}" \
  "${packer_template}" \
  "${artifacts}" \
  cluster

jq '
  .resource_changes += [{
    "address": "google_storage_bucket.reviewed",
    "mode": "managed",
    "type": "google_storage_bucket",
    "name": "reviewed",
    "change": {
      "actions": ["no-op"],
      "before": {"name": "reviewed"},
      "after": {"name": "reviewed"}
    }
  }]
' "${cluster_fixture}" >"${test_dir}/cluster-outside-noop.json"
expect_success "cluster-outside-noop" "${test_dir}/cluster-outside-noop.json" cluster

jq '
  .resource_changes += [{
    "address": "module.nomad.nomad_job.unreviewed",
    "mode": "managed",
    "type": "nomad_job",
    "name": "unreviewed",
    "change": {
      "actions": ["create"],
      "before": null,
      "after": {"jobspec": "job unreviewed {}"}
    }
  }]
' "${cluster_fixture}" >"${test_dir}/cluster-nomad-job.json"
expect_failure \
  "cluster-nomad-job" \
  "Nomad workload resources must be absent." \
  "${test_dir}/cluster-nomad-job.json" \
  "${policy}" \
  "${packer_template}" \
  "${artifacts}" \
  cluster

jq '
  (
    .resource_changes[]
    | select(.address == "module.cluster.google_compute_instance_template.api")
    | .change.after.disk[]
    | select(.boot == true)
    | .source_image
  ) = "projects/monad-code/global/images/unreviewed"
' "${cluster_fixture}" >"${test_dir}/cluster-template-source-image-drift.json"
expect_failure \
  "cluster-template-source-image-drift" \
  "invalid_template_source_images must be empty." \
  "${test_dir}/cluster-template-source-image-drift.json" \
  "${policy}" \
  "${packer_template}" \
  "${artifacts}" \
  cluster

expect_failure \
  "unknown-assertion-scope" \
  "Unknown workload plan assertion scope" \
  "${cluster_fixture}" \
  "${policy}" \
  "${packer_template}" \
  "${artifacts}" \
  unsupported

jq '
  (
    .resource_changes[]
    | select(
        .type == "google_compute_instance_template"
        and (
          .address
          | contains(".module.build_cluster[")
          or contains(".module.client_cluster[")
        )
      )
    | .change.after.disk[]
    | select(.disk_type == "pd-ssd")
    | .disk_type
  ) = "pd-balanced"
' "${fixture}" >"${test_dir}/balanced-ssd.json"
expect_success "balanced-ssd" "${test_dir}/balanced-ssd.json"

jq '
  (
    .resource_changes[]
    | select(.name == "clickhouse_pool")
    | .change.after.target_size
  ) = 1
' "${fixture}" >"${test_dir}/unexpected-clickhouse.json"
expect_failure \
  "unexpected-clickhouse" \
  "quota_violations must be empty." \
  "${test_dir}/unexpected-clickhouse.json"

jq '
  (
    .resource_changes[]
    | select(
        .address
        == "module.cluster.module.build_cluster[\"default\"].google_compute_region_instance_group_manager.pool"
      )
    | .change.after.update_policy[0].max_surge_fixed
  ) = 1
' "${fixture}" >"${test_dir}/worker-surge.json"
expect_failure \
  "worker-surge" \
  "automated_worker_server_surges must be empty." \
  "${test_dir}/worker-surge.json"

jq '
  (
    .resource_changes[]
    | select(.name == "server_pool")
    | .change.after.update_policy[0].max_surge_fixed
  ) = 1
' "${fixture}" >"${test_dir}/server-surge.json"
expect_failure \
  "server-surge" \
  "automated_worker_server_surges must be empty." \
  "${test_dir}/server-surge.json"

jq '
  (
    .resource_changes[]
    | select(.name == "server_pool")
    | .change.after.update_policy[0].max_unavailable_fixed
  ) = 2
' "${fixture}" >"${test_dir}/server-unavailable.json"
expect_failure \
  "server-unavailable" \
  "role maximum unavailable counts differ from policy." \
  "${test_dir}/server-unavailable.json"

jq '
  (
    .resource_changes[]
    | select(.name == "api_pool")
    | .change.after.update_policy[0].max_surge_fixed
  ) = 2
' "${fixture}" >"${test_dir}/quota-overflow.json"
expect_failure \
  "quota-overflow" \
  "quota_violations must be empty." \
  "${test_dir}/quota-overflow.json"

jq '
  (
    .resource_changes[]
    | select(.name == "server_pool")
    | .change.actions
  ) = ["delete", "create"]
' "${fixture}" >"${test_dir}/destructive-mig.json"
expect_failure \
  "destructive-mig" \
  "module.cluster compute mutations must be empty." \
  "${test_dir}/destructive-mig.json"

jq '
  .resource_changes += [{
    "address": "google_storage_bucket.unrelated",
    "mode": "managed",
    "type": "google_storage_bucket",
    "name": "unrelated",
    "change": {
      "actions": ["delete"],
      "before": {"name": "valuable"},
      "after": null
    }
  }]
' "${fixture}" >"${test_dir}/destructive-unreviewed.json"
expect_failure \
  "destructive-unreviewed" \
  "destructive_managed_resources must be empty." \
  "${test_dir}/destructive-unreviewed.json"

jq '
  .resource_changes += [{
    "address": "google_storage_bucket.forgotten",
    "mode": "managed",
    "type": "google_storage_bucket",
    "name": "forgotten",
    "change": {
      "actions": ["forget"],
      "before": {"name": "valuable"},
      "after": null
    }
  }]
' "${fixture}" >"${test_dir}/state-forget.json"
expect_failure \
  "state-forget" \
  "destructive_managed_resources must be empty." \
  "${test_dir}/state-forget.json"

jq '
  (
    .resource_changes[]
    | select(
        .address
        == "module.cluster.module.build_cluster[\"default\"].google_compute_region_instance_group_manager.pool"
      )
    | .change.actions
  ) = ["update"]
  |
  (
    .resource_changes[]
    | select(
        .address
        == "module.cluster.module.build_cluster[\"default\"].google_compute_region_instance_group_manager.pool"
      )
    | .change.before
  ) = {
    "target_size": 2,
    "update_policy": [{"max_surge_fixed": 0, "max_surge_percent": null}]
  }
' "${fixture}" >"${test_dir}/capacity-reduction.json"
expect_failure \
  "capacity-reduction" \
  "module.cluster compute mutations must be empty." \
  "${test_dir}/capacity-reduction.json"

jq '
  (
    .resource_changes[]
    | select(.name == "api_pool")
    | .change.actions
  ) = ["update"]
' "${fixture}" >"${test_dir}/unknown-previous-capacity.json"
expect_failure \
  "unknown-previous-capacity" \
  "module.cluster compute mutations must be empty." \
  "${test_dir}/unknown-previous-capacity.json"

jq '
  (
    .resource_changes[]
    | select(.name == "server_pool")
    | .change.after.update_policy[0].max_surge_fixed
  ) = null
  |
  (
    .resource_changes[]
    | select(.name == "server_pool")
    | .change.after_unknown.update_policy
  ) = true
' "${fixture}" >"${test_dir}/unknown-surge.json"
expect_failure \
  "unknown-surge" \
  "unresolved_surges must be empty." \
  "${test_dir}/unknown-surge.json"

jq '
  (
    .resource_changes[]
    | select(
        .address
        == "module.cluster.module.client_cluster[\"default\"].google_compute_instance_template.template"
      )
    | .change.after.machine_type
  ) = "n1-standard-4"
' "${fixture}" >"${test_dir}/machine-type-drift.json"
expect_failure \
  "machine-type-drift" \
  "role machine and disk resources differ from policy." \
  "${test_dir}/machine-type-drift.json"

jq '
  (
    .resource_changes[]
    | select(
        .address
        == "module.cluster.module.build_cluster[\"default\"].google_compute_instance_template.template"
      )
    | .change.after.disk[]
    | select(.disk_type == "pd-ssd")
    | .disk_size_gb
  ) = 110
' "${fixture}" >"${test_dir}/ssd-capacity-drift.json"
expect_failure \
  "ssd-capacity-drift" \
  "role machine and disk resources differ from policy." \
  "${test_dir}/ssd-capacity-drift.json"

jq '
  (
    .resource_changes[]
    | select(.address == "module.cluster.google_compute_instance_template.api")
    | .change.after.disk[0].disk_size_gb
  ) = 201
' "${fixture}" >"${test_dir}/standard-pd-drift.json"
expect_failure \
  "standard-pd-drift" \
  "role machine and disk resources differ from policy." \
  "${test_dir}/standard-pd-drift.json"

jq '
  (
    .resource_changes[]
    | select(
        .address
        == "module.cluster.module.client_cluster[\"default\"].google_compute_instance_template.template"
      )
    | .change.after.disk[]
    | select(.disk_type == "local-ssd")
    | .disk_size_gb
  ) = 750
' "${fixture}" >"${test_dir}/local-ssd-drift.json"
expect_failure \
  "local-ssd-drift" \
  "role machine and disk resources differ from policy." \
  "${test_dir}/local-ssd-drift.json"

jq '
  (
    .resource_changes[]
    | select(.address == "module.cluster.google_compute_instance_template.api")
    | .change.after.network_interface[0].access_config
  ) = []
' "${fixture}" >"${test_dir}/public-ip-drift.json"
expect_failure \
  "public-ip-drift" \
  "role machine and disk resources differ from policy." \
  "${test_dir}/public-ip-drift.json"

jq '
  (
    .resource_changes[]
    | select(
        .address
        == "module.cluster.module.client_cluster[\"default\"].google_compute_instance_template.template"
      )
    | .change.after.disk[0].disk_type
  ) = "hyperdisk-balanced"
' "${fixture}" >"${test_dir}/unknown-disk-type.json"
expect_failure \
  "unknown-disk-type" \
  "invalid_template_disks must be empty." \
  "${test_dir}/unknown-disk-type.json"

jq '
  (
    .resource_changes[]
    | select(.address == "module.cluster.google_compute_instance_template.server")
    | .change.after_unknown.disk
  ) = true
' "${fixture}" >"${test_dir}/unknown-disk.json"
expect_failure \
  "unknown-disk" \
  "unresolved_templates must be empty." \
  "${test_dir}/unknown-disk.json"

jq '
  (
    .resource_changes[]
    | select(
        .address
        == "module.cluster.module.client_cluster[\"default\"].google_compute_instance_template.template"
      )
    | .change.after.disk[]
    | select(.disk_type == "local-ssd")
  ) |= del(.type)
' "${fixture}" >"${test_dir}/unknown-local-ssd-type.json"
expect_failure \
  "unknown-local-ssd-type" \
  "invalid_template_disks must be empty." \
  "${test_dir}/unknown-local-ssd-type.json"

jq '
  .resource_changes += [
    {
      "address": "module.cluster.google_compute_instance_template.unreviewed",
      "mode": "managed",
      "type": "google_compute_instance_template",
      "name": "unreviewed",
      "change": {
        "actions": ["create"],
        "after": {
          "machine_type": "e2-standard-2",
          "disk": [{
            "disk_size_gb": 20,
            "disk_type": "pd-ssd",
            "type": "PERSISTENT"
          }],
          "network_interface": [{"access_config": [{}]}]
        }
      }
    }
  ]
' "${fixture}" >"${test_dir}/unknown-template.json"
expect_failure \
  "unknown-template" \
  "module.cluster compute mutations must be empty." \
  "${test_dir}/unknown-template.json"

jq '
  .resource_changes += [
    {
      "address": "module.cluster.google_compute_disk.unreviewed",
      "mode": "managed",
      "type": "google_compute_disk",
      "name": "unreviewed",
      "change": {
        "actions": ["create"],
        "after": {
          "size": 10,
          "type": "pd-ssd"
        }
      }
    }
  ]
' "${fixture}" >"${test_dir}/unknown-resource.json"
expect_failure \
  "unknown-resource" \
  "module.cluster compute mutations must be empty." \
  "${test_dir}/unknown-resource.json"

jq '
  (
    .resource_changes[]
    | select(.address == "google_sql_database_instance.operator_canary")
    | .change.after.settings[0].ip_configuration[0].ssl_mode
  ) = "ALLOW_UNENCRYPTED_AND_ENCRYPTED"
' "${fixture}" >"${test_dir}/cloud-sql-plaintext.json"
expect_failure \
  "cloud-sql-plaintext" \
  "invalid_cloud_sql_resources must be empty." \
  "${test_dir}/cloud-sql-plaintext.json"

jq '
  (
    .resource_changes[]
    | select(.address == "google_sql_database_instance.operator_canary")
    | .change.after.settings[0].ip_configuration[0].ipv4_enabled
  ) = true
' "${fixture}" >"${test_dir}/cloud-sql-public-ip.json"
expect_failure \
  "cloud-sql-public-ip" \
  "invalid_cloud_sql_resources must be empty." \
  "${test_dir}/cloud-sql-public-ip.json"

jq '
  (
    .resource_changes[]
    | select(.address == "google_sql_database_instance.operator_canary")
    | .change.after.settings[0].backup_configuration[0].point_in_time_recovery_enabled
  ) = false
' "${fixture}" >"${test_dir}/cloud-sql-no-pitr.json"
expect_failure \
  "cloud-sql-no-pitr" \
  "invalid_cloud_sql_resources must be empty." \
  "${test_dir}/cloud-sql-no-pitr.json"

jq '
  (
    .resource_changes[]
    | select(.address == "google_sql_database_instance.operator_canary")
    | .change.after.deletion_protection
  ) = false
' "${fixture}" >"${test_dir}/cloud-sql-no-delete-protection.json"
expect_failure \
  "cloud-sql-no-delete-protection" \
  "invalid_cloud_sql_resources must be empty." \
  "${test_dir}/cloud-sql-no-delete-protection.json"

jq '
  (
    .resource_changes[]
    | select(.address == "terraform_data.cloud_sql_connection_budget")
    | .change.after.input.db_max_open_connections
  ) = 7
  |
  (
    .resource_changes[]
    | select(.address == "terraform_data.cloud_sql_connection_budget")
    | .change.after.input.maximum_concurrent_connections
  ) = 20
' "${fixture}" >"${test_dir}/cloud-sql-pool-over-budget.json"
expect_failure \
  "cloud-sql-pool-over-budget" \
  "invalid_cloud_sql_resources must be empty." \
  "${test_dir}/cloud-sql-pool-over-budget.json"

jq '
  (
    .resource_changes[]
    | select(.address == "terraform_data.cloud_sql_connection_budget")
    | .change.after.input.api_server_count
  ) = 2
  |
  (
    .resource_changes[]
    | select(.address == "terraform_data.cloud_sql_connection_budget")
    | .change.after.input.maximum_concurrent_connections
  ) = 28
' "${fixture}" >"${test_dir}/cloud-sql-api-replica-drift.json"
expect_failure \
  "cloud-sql-api-replica-drift" \
  "invalid_cloud_sql_resources must be empty." \
  "${test_dir}/cloud-sql-api-replica-drift.json"

jq '
  (
    .resource_changes[]
    | select(.address == "terraform_data.cloud_sql_connection_budget")
    | .change.after.input.dashboard_api_count
  ) = 1
  |
  (
    .resource_changes[]
    | select(.address == "terraform_data.cloud_sql_connection_budget")
    | .change.after.input.maximum_concurrent_connections
  ) = 35
' "${fixture}" >"${test_dir}/cloud-sql-dashboard-enabled.json"
expect_failure \
  "cloud-sql-dashboard-enabled" \
  "invalid_cloud_sql_resources must be empty." \
  "${test_dir}/cloud-sql-dashboard-enabled.json"

jq '
  (
    .resource_changes[]
    | select(.address == "google_service_networking_connection.cloud_sql")
    | .change.after.network
  ) = "projects/operator-canary/global/networks/wrong"
' "${fixture}" >"${test_dir}/cloud-sql-network-mismatch.json"
expect_failure \
  "cloud-sql-network-mismatch" \
  "invalid_cloud_sql_resources must be empty." \
  "${test_dir}/cloud-sql-network-mismatch.json"

jq '
  (
    .resource_changes[]
    | select(.address == "google_sql_database.operator_canary")
    | .change.after.project
  ) = "wrong-project"
' "${fixture}" >"${test_dir}/cloud-sql-project-mismatch.json"
expect_failure \
  "cloud-sql-project-mismatch" \
  "invalid_cloud_sql_resources must be empty." \
  "${test_dir}/cloud-sql-project-mismatch.json"

jq '
  (
    .resource_changes[]
    | select(.address == "google_project_service_identity.cloud_sql")
    | .change.after.email
  ) = "service-agent@operator-canary.iam.gserviceaccount.com"
  |
  del(
    .resource_changes[]
    | select(.address == "google_project_service_identity.cloud_sql")
    | .change.after_unknown.email
  )
  |
  (
    .resource_changes[]
    | select(.address == "google_project_iam_member.cloud_sql_service_agent")
    | .change.after.member
  ) = "serviceAccount:wrong@operator-canary.iam.gserviceaccount.com"
  |
  del(
    .resource_changes[]
    | select(.address == "google_project_iam_member.cloud_sql_service_agent")
    | .change.after_unknown.member
  )
' "${fixture}" >"${test_dir}/cloud-sql-iam-member-mismatch.json"
expect_failure \
  "cloud-sql-iam-member-mismatch" \
  "invalid_cloud_sql_resources must be empty." \
  "${test_dir}/cloud-sql-iam-member-mismatch.json"

jq '
  (
    .resource_changes[]
    | select(.address == "google_sql_user.operator_canary")
    | .change.after.password
  ) = "password"
  |
  del(
    .resource_changes[]
    | select(.address == "google_sql_user.operator_canary")
    | .change.after_unknown.password
  )
' "${fixture}" >"${test_dir}/cloud-sql-literal-password.json"
expect_failure \
  "cloud-sql-literal-password" \
  "invalid_cloud_sql_resources must be empty." \
  "${test_dir}/cloud-sql-literal-password.json"

jq '
  (
    .resource_changes[]
    | select(.address == "google_secret_manager_secret_version.postgres_connection_string")
    | .change.after.secret
  ) = "projects/other/secrets/hostile"
  |
  (
    .resource_changes[]
    | select(.address == "google_secret_manager_secret_version.postgres_connection_string")
    | .change.after.secret_data
  ) = "plaintext"
  |
  del(
    .resource_changes[]
    | select(.address == "google_secret_manager_secret_version.postgres_connection_string")
    | .change.after_unknown.secret_data
  )
' "${fixture}" >"${test_dir}/cloud-sql-hostile-secret.json"
expect_failure \
  "cloud-sql-hostile-secret" \
  "invalid_cloud_sql_resources must be empty." \
  "${test_dir}/cloud-sql-hostile-secret.json"

jq '
  .resource_changes += [
    {
      "address": "google_sql_database_instance.unreviewed",
      "mode": "managed",
      "type": "google_sql_database_instance",
      "name": "unreviewed",
      "change": {
        "actions": ["create"],
        "after": {}
      }
    }
  ]
' "${fixture}" >"${test_dir}/unknown-cloud-sql.json"
expect_failure \
  "unknown-cloud-sql" \
  "unknown_cloud_sql_resources must be empty." \
  "${test_dir}/unknown-cloud-sql.json"

jq '
  .resource_changes |= map(
    select(
      .address
      != "google_secret_manager_secret_version.postgres_connection_string"
    )
  )
' "${fixture}" >"${test_dir}/missing-cloud-sql-secret-version.json"
expect_failure \
  "missing-cloud-sql-secret-version" \
  "missing_or_duplicate_cloud_sql_resources must be empty." \
  "${test_dir}/missing-cloud-sql-secret-version.json"

jq '
  (
    .resource_changes[]
    | select(.address == "google_sql_database_instance.operator_canary")
    | .change.actions
  ) = ["delete"]
' "${fixture}" >"${test_dir}/destructive-cloud-sql.json"
expect_failure \
  "destructive-cloud-sql" \
  "destructive_cloud_sql_resources must be empty." \
  "${test_dir}/destructive-cloud-sql.json"

jq '
  (
    .planned_values.root_module
    | recurse(.child_modules[]?)
    | .resources[]?
    | select(
        .address
        == "module.cluster.data.google_compute_image.api_source_image"
      )
    | .values.name
  ) = "e2b-orch-unreviewed"
' "${fixture}" >"${test_dir}/orchestrator-image-drift.json"
expect_failure \
  "orchestrator-image-drift" \
  "invalid_orchestrator_images must be empty." \
  "${test_dir}/orchestrator-image-drift.json"

jq '
  (
    .resource_changes[]
    | select(.address == "module.cluster.google_compute_instance_template.api")
    | .change.after.disk[]
    | select(.boot == true)
    | .source_image
  ) = "projects/monad-code/global/images/unreviewed"
' "${fixture}" >"${test_dir}/template-source-image-drift.json"
expect_failure \
  "template-source-image-drift" \
  "invalid_template_source_images must be empty." \
  "${test_dir}/template-source-image-drift.json"

jq '
  (
    .planned_values.root_module
    | recurse(.child_modules[]?)
    | .resources[]?
    | select(
        .address
        == "module.nomad.data.google_artifact_registry_docker_image.api_image"
      )
    | .values.self_link
  ) = "us-east4-docker.pkg.dev/monad-code/e2b-core/api@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
' "${fixture}" >"${test_dir}/core-image-drift.json"
expect_failure \
  "core-image-drift" \
  "invalid_core_images must be empty." \
  "${test_dir}/core-image-drift.json"

jq '
  (
    .resource_changes[]
    | select(.address == "module.nomad.module.api.nomad_job.api")
    | .change.after.jobspec
  ) = "# us-east4-docker.pkg.dev/monad-code/e2b-core/api@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n# us-east4-docker.pkg.dev/monad-code/e2b-core/db-migrator@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\nimage = \"malicious.example.invalid/api@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb\""
' "${fixture}" >"${test_dir}/core-job-drift.json"
expect_failure \
  "core-job-drift" \
  "invalid_core_jobs must be empty." \
  "${test_dir}/core-job-drift.json"

jq '
  (
    .planned_values.root_module
    | recurse(.child_modules[]?)
    | .resources[]?
    | select(
        .address
        == "module.nomad.data.google_storage_bucket_object.template_manager"
      )
    | .values.generation
  ) = 9999
' "${fixture}" >"${test_dir}/job-binary-generation-drift.json"
expect_failure \
  "job-binary-generation-drift" \
  "invalid_job_binary_objects must be empty." \
  "${test_dir}/job-binary-generation-drift.json"

jq '
  (
    .planned_values.root_module.child_modules[].resources
  ) |= map(
    select(
      .address
      != "module.nomad.data.google_storage_bucket_object.filestore_cleanup"
    )
  )
' "${fixture}" >"${test_dir}/missing-job-binary-object.json"
expect_failure \
  "missing-job-binary-object" \
  "missing_or_duplicate_job_binary_objects must be empty." \
  "${test_dir}/missing-job-binary-object.json"

jq '
  (
    .resource_changes[]
    | select(
        .address
        == "module.nomad.module.template_manager.nomad_job.template_manager"
      )
    | .change.after.jobspec
  ) = "source = \"gcs::https://www.googleapis.com/storage/v1/monad-code-fc-env-pipeline/template-manager\""
' "${fixture}" >"${test_dir}/job-binary-unpinned-url.json"
expect_failure \
  "job-binary-unpinned-url" \
  "invalid_job_binary_jobs must be empty." \
  "${test_dir}/job-binary-unpinned-url.json"

jq '
  .resource_changes |= map(
    select(
      .address
      != "module.nomad.module.orchestrator[0].nomad_job.orchestrator"
    )
  )
' "${fixture}" >"${test_dir}/missing-required-job-binary-job.json"
expect_failure \
  "missing-required-job-binary-job" \
  "missing_or_duplicate_job_binary_jobs must be empty." \
  "${test_dir}/missing-required-job-binary-job.json"

jq '
  .resource_changes |= map(
    select(
      .address
      != "module.nomad.nomad_job.clean_nfs_cache[0]"
    )
  )
' "${fixture}" >"${test_dir}/optional-clean-nfs-job-absent.json"
expect_success \
  "optional-clean-nfs-job-absent" \
  "${test_dir}/optional-clean-nfs-job-absent.json"

jq '.schema_version = 1' \
  "${artifacts}" >"${test_dir}/legacy-workload-artifacts.json"
expect_failure \
  "legacy-workload-artifacts" \
  "Resolved workload artifacts are invalid" \
  "${fixture}" \
  "${policy}" \
  "${packer_template}" \
  "${test_dir}/legacy-workload-artifacts.json"

jq '
  (
    .planned_values.root_module.child_modules[].resources
  ) |= map(
    select(
      .address
      != "module.nomad.data.google_artifact_registry_docker_image.client_proxy_image"
    )
  )
' "${fixture}" >"${test_dir}/missing-core-image.json"
expect_failure \
  "missing-core-image" \
  "missing_or_duplicate_core_images must be empty." \
  "${test_dir}/missing-core-image.json"

jq '.quota_limits.global_vcpus = 64' \
  "${policy}" >"${test_dir}/quota-policy-drift.json"
expect_failure \
  "quota-policy-drift" \
  "Workload topology policy is invalid or differs from reviewed quota limits" \
  "${fixture}" \
  "${test_dir}/quota-policy-drift.json"

jq '.transient_reserve.vcpus = 8' \
  "${policy}" >"${test_dir}/packer-policy-drift.json"
expect_failure \
  "packer-policy-drift" \
  "Workload topology policy is invalid or differs from reviewed quota limits" \
  "${fixture}" \
  "${test_dir}/packer-policy-drift.json"

sed \
  's/machine_type = local.quota_reserve.machine_type/machine_type = "n1-standard-8"/' \
  "${packer_template}" >"${test_dir}/packer-drift.pkr.hcl"
expect_failure \
  "packer-template-drift" \
  "Packer template must contain exactly one machine-type reserve assignment" \
  "${fixture}" \
  "${policy}" \
  "${test_dir}/packer-drift.pkr.hcl"

"${packer_assertion_script}" "${policy}" "${packer_template}" >/dev/null

printf 'Workload plan assertion fixtures passed.\n'
