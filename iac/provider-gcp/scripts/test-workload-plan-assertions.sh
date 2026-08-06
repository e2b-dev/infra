#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
assertion_script="${script_dir}/assert-workload-plan.sh"
packer_assertion_script="${script_dir}/assert-packer-reserve.sh"
fixture="${script_dir}/testdata/minimal-workload-plan.json"
cloud_sql_fixture="${script_dir}/testdata/cloud-sql-workload-resources.json"
cloud_sql_project_state="${script_dir}/testdata/cloud-sql-project-state.json"
artifact_bindings_fixture="${script_dir}/testdata/workload-artifact-plan-bindings.json"
artifact_prior_fixture="${script_dir}/testdata/workload-artifact-prior-state-bindings.json"
artifacts="${script_dir}/testdata/workload-artifacts.json"
policy="${script_dir}/../topology/minimal-workload-policy.json"
packer_template="${script_dir}/../nomad-cluster-disk-image/main.pkr.hcl"
cloud_sql_config="${script_dir}/../cloud-sql.tf"
nomad_config="${script_dir}/../nomad/main.tf"
reverse_proxy_store="${script_dir}/../../../packages/docker-reverse-proxy/internal/handlers/store.go"
dashboard_api_main="${script_dir}/../../../packages/dashboard-api/main.go"
database_migrator="${script_dir}/../../../packages/db/scripts/migrator.go"
test_dir="$(mktemp -d)"
trap 'rm -rf "${test_dir}"' EXIT

fake_terraform="${test_dir}/terraform"
cp "${script_dir}/testdata/fake-terraform.sh" "${fake_terraform}"
chmod 0700 "${fake_terraform}"

candidate_password_secret_block="$(
  awk '
    /^resource "google_secret_manager_secret" "cloud_sql_invited_beta_password"/ {
      capture = 1
    }
    capture { print }
    capture && /^}/ { exit }
  ' "${cloud_sql_config}"
)"

jq \
  --slurpfile cloud_sql "${cloud_sql_fixture}" \
  --slurpfile cloud_sql_project "${cloud_sql_project_state}" \
  --slurpfile artifact_bindings "${artifact_bindings_fixture}" \
  --slurpfile artifact_prior "${artifact_prior_fixture}" '
  .resource_changes += (
    $cloud_sql[0]
    + $artifact_bindings[0].resource_changes
  )
  | .prior_state = {
      format_version: "1.0",
      values: {
        root_module: {
          child_modules: (
            [
              {
                address: "module.init",
                resources: $cloud_sql_project
              }
            ]
            + $artifact_prior[0].values.root_module.child_modules
          )
        }
      }
    }
  | .configuration = $artifact_bindings[0].configuration
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
grep -F 'resource "google_sql_database_instance" "invited_beta"' \
  "${cloud_sql_config}" >/dev/null
grep -F 'name             = "${var.prefix}postgres-beta"' \
  "${cloud_sql_config}" >/dev/null
grep -F 'password = random_password.cloud_sql_invited_beta.result' \
  "${cloud_sql_config}" >/dev/null
grep -F 'resource "google_secret_manager_secret" "cloud_sql_invited_beta_password"' \
  "${cloud_sql_config}" >/dev/null
grep -E 'secret_id[[:space:]]*=[[:space:]]*"\$\{var\.prefix\}postgres-beta-password"' \
  <<<"${candidate_password_secret_block}" >/dev/null
grep -E 'deletion_protection[[:space:]]*=[[:space:]]*true' \
  <<<"${candidate_password_secret_block}" >/dev/null
grep -F 'secret_data = random_password.cloud_sql_invited_beta.result' \
  "${cloud_sql_config}" >/dev/null
grep -F 'secret = module.init.postgres_connection_string_secret_name' \
  "${cloud_sql_config}" >/dev/null
grep -F 'member  = "serviceAccount:${google_project_service_identity.cloud_sql.email}"' \
  "${cloud_sql_config}" >/dev/null
grep -F 'member  = "serviceAccount:${google_project_service_identity.service_networking.email}"' \
  "${cloud_sql_config}" >/dev/null
grep -F 'prevent_destroy = true' \
  "${cloud_sql_config}" >/dev/null
test "$(
  grep -Ec \
    '^[[:space:]]+(batch|service|sysbatch|system)_scheduler_enabled[[:space:]]*=[[:space:]]*false$' \
    "${nomad_config}"
)" -eq 4
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

  grep -F '"global_vcpus":44' "${output_path}" >/dev/null
  grep -F '"regional_cpus":44' "${output_path}" >/dev/null
  grep -F '"instances":10' "${output_path}" >/dev/null
  grep -F '"pd_ssd_gb":380' "${output_path}" >/dev/null
  grep -F '"pd_standard_gb":600' "${output_path}" >/dev/null
  grep -F '"local_ssd_gb":1125' "${output_path}" >/dev/null
  grep -F '"regional_public_ips":3' "${output_path}" >/dev/null
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
    | select(.address | startswith("module.cluster.module.network.google_compute_address.nat_ips["))
    | .change.after.region
  ) = "us-east4"
' "${fixture}" >"${test_dir}/short-nat-region.json"
expect_success "short-nat-region" "${test_dir}/short-nat-region.json"

jq '
  (
    .resource_changes[]
    | select(.address == "module.cluster.module.network.google_compute_address.nat_ips[0]")
    | .change.after.region
  ) = "us-west1"
' "${fixture}" >"${test_dir}/wrong-nat-region.json"
expect_failure \
  "wrong-nat-region" \
  "invalid_fixed_regional_public_ip_resources must be empty." \
  "${test_dir}/wrong-nat-region.json"

jq '
  .resource_changes |= map(
    select(
      .address
      != "module.cluster.module.network.google_compute_address.nat_ips[1]"
    )
  )
' "${fixture}" >"${test_dir}/missing-nat-ip.json"
expect_failure \
  "missing-nat-ip" \
  "fixed regional public IPs differ from policy." \
  "${test_dir}/missing-nat-ip.json"

jq '
  .resource_changes += [
    {
      address: "module.init.data.google_project.current",
      mode: "data",
      type: "google_project",
      name: "current",
      change: {
        actions: ["read"],
        before: {
          number: "123456789",
          project_id: "operator-canary"
        },
        after: {
          number: "123456789",
          project_id: "operator-canary"
        },
        after_unknown: {}
      }
    }
  ]
' "${fixture}" >"${test_dir}/refreshed-project-identity.json"
expect_success \
  "refreshed-project-identity" \
  "${test_dir}/refreshed-project-identity.json"

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
    "address": "module.cluster.terraform_data.network_hardening_rollout_completion_server[0]",
    "mode": "managed",
    "type": "terraform_data",
    "name": "network_hardening_rollout_completion_server",
    "change": {
      "actions": ["delete", "create"],
      "before": {"input": "server"},
      "after": {"input": "server"}
    }
  }]
' "${cluster_fixture}" >"${test_dir}/cluster-network-hardening-completion.json"
expect_success \
  "cluster-network-hardening-completion" \
  "${test_dir}/cluster-network-hardening-completion.json" \
  cluster

jq '
  (
    .resource_changes[]
    | select(
        .address
        == "module.cluster.module.client_cluster[\"default\"].google_compute_instance_template.template"
      )
    | .change
  ) |= (
    .before = .after
    | .actions = ["create", "delete"]
  )
' "${cluster_fixture}" >"${test_dir}/cluster-rolling-client-template.json"
expect_success \
  "cluster-rolling-client-template" \
  "${test_dir}/cluster-rolling-client-template.json" \
  cluster

jq '
  .resource_changes += [{
    "address": "module.cluster.google_storage_bucket_object.setup_config_objects[\"scripts/run-nomad.sh\"]",
    "mode": "managed",
    "type": "google_storage_bucket_object",
    "name": "setup_config_objects",
    "index": "scripts/run-nomad.sh",
    "change": {
      "actions": ["create", "delete"],
      "before": {
        "bucket": "operator-instance-setup",
        "deletion_policy": "DELETE",
        "name": "run-nomad-abc12.sh"
      },
      "after": {
        "bucket": "operator-instance-setup",
        "deletion_policy": "ABANDON",
        "name": "run-nomad-def34.sh"
      },
      "after_unknown": {}
    }
  }]
' "${cluster_fixture}" >"${test_dir}/cluster-content-addressed-setup-object.json"
expect_success \
  "cluster-content-addressed-setup-object" \
  "${test_dir}/cluster-content-addressed-setup-object.json" \
  cluster

jq '
  (
    .resource_changes[]
    | select(.type == "google_storage_bucket_object")
    | .change.after.deletion_policy
  ) = "DELETE"
' "${test_dir}/cluster-content-addressed-setup-object.json" \
  >"${test_dir}/cluster-deleting-setup-object.json"
expect_failure \
  "cluster-deleting-setup-object" \
  "destructive_managed_resources must be empty." \
  "${test_dir}/cluster-deleting-setup-object.json" \
  "${policy}" \
  "${packer_template}" \
  "${artifacts}" \
  cluster

jq '
  (
    .resource_changes[]
    | select(.type == "google_storage_bucket_object")
    | .change.after.name
  ) = "run-consul-def34.sh"
' "${test_dir}/cluster-content-addressed-setup-object.json" \
  >"${test_dir}/cluster-mismatched-setup-object.json"
expect_failure \
  "cluster-mismatched-setup-object" \
  "destructive_managed_resources must be empty." \
  "${test_dir}/cluster-mismatched-setup-object.json" \
  "${policy}" \
  "${packer_template}" \
  "${artifacts}" \
  cluster

jq '
  (
    .resource_changes[]
    | select(
        .address
        == "module.cluster.module.client_cluster[\"default\"].google_compute_instance_template.template"
      )
    | .change
  ) |= (
    .before = .after
    | .actions = ["delete", "create"]
  )
' "${cluster_fixture}" >"${test_dir}/cluster-destroy-before-create-template.json"
expect_failure \
  "cluster-destroy-before-create-template" \
  "destructive_managed_resources must be empty." \
  "${test_dir}/cluster-destroy-before-create-template.json" \
  "${policy}" \
  "${packer_template}" \
  "${artifacts}" \
  cluster

jq '
  (
    .resource_changes[]
    | select(.address == "module.cluster.google_compute_instance_template.api")
    | .change
  ) as $reviewed_change
  | .resource_changes += [{
    "address": "module.cluster.google_compute_instance_template.unreviewed",
    "mode": "managed",
    "type": "google_compute_instance_template",
    "name": "unreviewed",
    "change": (
      $reviewed_change
      | .before = .after
      | .actions = ["create", "delete"]
    )
  }]
' "${cluster_fixture}" >"${test_dir}/cluster-unreviewed-template-replacement.json"
expect_failure \
  "cluster-unreviewed-template-replacement" \
  "unknown_templates must be empty." \
  "${test_dir}/cluster-unreviewed-template-replacement.json" \
  "${policy}" \
  "${packer_template}" \
  "${artifacts}" \
  cluster

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
  "role maximum instance counts differ from policy." \
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
  "automated_rollout_surges must be empty." \
  "${test_dir}/worker-surge.json"

jq '
  (
    .resource_changes[]
    | select(.name == "server_pool")
    | .change.after.update_policy[0].max_surge_fixed
  ) = 2
' "${fixture}" >"${test_dir}/server-surge.json"
expect_failure \
  "server-surge" \
  "automated_rollout_surges must be empty." \
  "${test_dir}/server-surge.json"

jq '
  (
    .resource_changes[]
    | select(.name == "server_pool")
    | .change.after.update_policy[0].max_unavailable_fixed
  ) = 1
' "${fixture}" >"${test_dir}/server-unavailable.json"
expect_failure \
  "server-unavailable" \
  "unsafe_server_control_plane_rollouts must be empty." \
  "${test_dir}/server-unavailable.json"

jq '
  (
    .resource_changes[]
    | select(.name == "server_pool")
    | .change.after.update_policy[0].max_unavailable_percent
  ) = 1
' "${fixture}" >"${test_dir}/server-percentage-unavailable.json"
expect_failure \
  "server-percentage-unavailable" \
  "percentage_max_unavailable must be empty." \
  "${test_dir}/server-percentage-unavailable.json"

jq '
  (
    .resource_changes[]
    | select(.name == "server_pool")
    | .change.after_unknown.update_policy
  ) = [{"max_unavailable_percent":true}]
' "${fixture}" >"${test_dir}/server-unknown-percentage-unavailable.json"
expect_failure \
  "server-unknown-percentage-unavailable" \
  "unresolved_max_unavailable must be empty." \
  "${test_dir}/server-unknown-percentage-unavailable.json"

jq '
  (
    .resource_changes[]
    | select(.name == "server_pool")
    | .change.after.update_policy[0].replacement_method
  ) = "RECREATE"
' "${fixture}" >"${test_dir}/server-recreate.json"
expect_failure \
  "server-recreate" \
  "unsafe_server_control_plane_rollouts must be empty." \
  "${test_dir}/server-recreate.json"

jq '
  (
    .resource_changes[]
    | select(.name == "server_pool")
    | .change.after.update_policy[0].min_ready_sec
  ) = 119
' "${fixture}" >"${test_dir}/server-short-ready-window.json"
expect_failure \
  "server-short-ready-window" \
  "unsafe_server_control_plane_rollouts must be empty." \
  "${test_dir}/server-short-ready-window.json"

jq '
  (
    .resource_changes[]
    | select(.name == "server_pool")
    | .change.after.update_policy[0].min_ready_sec
  ) = 121
' "${fixture}" >"${test_dir}/server-unreviewed-ready-window.json"
expect_failure \
  "server-unreviewed-ready-window" \
  "unsafe_server_control_plane_rollouts must be empty." \
  "${test_dir}/server-unreviewed-ready-window.json"

jq '
  (
    .resource_changes[]
    | select(.name == "server_pool")
    | .change.after.update_policy[0].type
  ) = "OPPORTUNISTIC"
' "${fixture}" >"${test_dir}/server-opportunistic.json"
expect_failure \
  "server-opportunistic" \
  "unsafe_server_control_plane_rollouts must be empty." \
  "${test_dir}/server-opportunistic.json"

jq '
  (
    .resource_changes[]
    | select(.name == "server_pool")
    | .change.after.update_policy[0].minimal_action
  ) = "RESTART"
' "${fixture}" >"${test_dir}/server-restart.json"
expect_failure \
  "server-restart" \
  "unsafe_server_control_plane_rollouts must be empty." \
  "${test_dir}/server-restart.json"

jq '
  (
    .resource_changes[]
    | select(.name == "server_pool")
    | .change.after
  ) |= (
    .update_policy[0].max_unavailable_fixed = 1
    | del(.distribution_policy_zones)
  )
' "${fixture}" >"${test_dir}/regional-mig-without-single-zone.json"
expect_failure \
  "regional-mig-without-single-zone" \
  "invalid_single_unavailable_regional_migs must be empty." \
  "${test_dir}/regional-mig-without-single-zone.json"

jq '
  (
    .resource_changes[]
    | select(.name == "server_pool")
    | .change.after.distribution_policy_zones
  ) = ["us-east4-a", "us-east4-b", "us-east4-c"]
' "${fixture}" >"${test_dir}/regional-mig-surge-below-zone-count.json"
expect_failure \
  "regional-mig-surge-below-zone-count" \
  "invalid_fixed_surge_regional_migs must be empty." \
  "${test_dir}/regional-mig-surge-below-zone-count.json"

jq '
  (
    .resource_changes[]
    | select(.address == "module.cluster.google_compute_health_check.server_nomad_check")
    | .change.after.http_health_check[0].port
  ) = 4646
' "${fixture}" >"${test_dir}/server-leader-only-health.json"
expect_failure \
  "server-leader-only-health" \
  "unsafe_server_voter_health_checks must be empty." \
  "${test_dir}/server-leader-only-health.json"

jq '
  (
    .resource_changes[]
    | select(.address == "module.cluster.google_compute_health_check.server_nomad_check")
    | .change.after.http_health_check[0].request_path
  ) = "/v1/agent/health"
' "${fixture}" >"${test_dir}/server-wrong-health-path.json"
expect_failure \
  "server-wrong-health-path" \
  "unsafe_server_voter_health_checks must be empty." \
  "${test_dir}/server-wrong-health-path.json"

jq '
  (
    .resource_changes[]
    | select(.name == "server_pool")
    | .change.after.auto_healing_policies[0].initial_delay_sec
  ) = 0
' "${fixture}" >"${test_dir}/server-unsafe-autoheal-delay.json"
expect_failure \
  "server-unsafe-autoheal-delay" \
  "unsafe_server_voter_health_checks must be empty." \
  "${test_dir}/server-unsafe-autoheal-delay.json"

jq '
  (
    .resource_changes[]
    | select(.name == "server_pool")
    | .change.after.auto_healing_policies[0].health_check
  ) = "https://www.googleapis.com/compute/v1/projects/monad-code/global/healthChecks/permissive-agent-health"
' "${fixture}" >"${test_dir}/server-wrong-autoheal-health-check.json"
expect_failure \
  "server-wrong-autoheal-health-check" \
  "unsafe_server_voter_health_checks must be empty." \
  "${test_dir}/server-wrong-autoheal-health-check.json"

jq '
  (
    .resource_changes[]
    | select(.name == "server_pool")
    | .change.after_unknown.auto_healing_policies
  ) = [{"health_check":true}]
' "${fixture}" >"${test_dir}/server-unknown-autoheal-health-check.json"
expect_failure \
  "server-unknown-autoheal-health-check" \
  "unsafe_server_voter_health_checks must be empty." \
  "${test_dir}/server-unknown-autoheal-health-check.json"

jq '
  (
    .resource_changes[]
    | select(.name == "server_pool")
    | .change.after.instance_lifecycle_policy[0].on_failed_health_check
  ) = "REPAIR"
' "${fixture}" >"${test_dir}/server-health-triggered-repair.json"
expect_failure \
  "server-health-triggered-repair" \
  "unsafe_server_failure_repair_policies must be empty." \
  "${test_dir}/server-health-triggered-repair.json"

jq '
  (
    .resource_changes[]
    | select(.name == "server_pool")
    | .change.after.instance_lifecycle_policy[0].default_action_on_failure
  ) = "DO_NOTHING"
' "${fixture}" >"${test_dir}/server-disabled-infrastructure-repair.json"
expect_failure \
  "server-disabled-infrastructure-repair" \
  "unsafe_server_failure_repair_policies must be empty." \
  "${test_dir}/server-disabled-infrastructure-repair.json"

jq '
  (
    .resource_changes[]
    | select(.name == "server_pool")
    | .change.after.instance_lifecycle_policy[0].force_update_on_repair
  ) = "YES"
' "${fixture}" >"${test_dir}/server-forced-update-on-repair.json"
expect_failure \
  "server-forced-update-on-repair" \
  "unsafe_server_failure_repair_policies must be empty." \
  "${test_dir}/server-forced-update-on-repair.json"

jq '
  (
    .resource_changes[]
    | select(.name == "server_pool")
    | .change.after_unknown.instance_lifecycle_policy
  ) = [true]
' "${fixture}" >"${test_dir}/server-unknown-failure-repair-policy.json"
expect_failure \
  "server-unknown-failure-repair-policy" \
  "unsafe_server_failure_repair_policies must be empty." \
  "${test_dir}/server-unknown-failure-repair-policy.json"

jq '
  (
    .resource_changes[]
    | select(.name == "api_pool")
    | .change.after.update_policy[0].max_surge_fixed
  ) = 2
' "${fixture}" >"${test_dir}/quota-overflow.json"
expect_failure \
  "quota-overflow" \
  "role rollout surge counts differ from policy." \
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
    "address": "module.nomad.module.orchestrator[0].random_id.orchestrator_job",
    "mode": "managed",
    "type": "random_id",
    "name": "orchestrator_job",
    "change": {
      "actions": ["delete", "create"],
      "before": {"id": "old-rollout-marker"},
      "after": {"id": null}
    }
  }]
' "${fixture}" >"${test_dir}/orchestrator-rollout-marker.json"
expect_success \
  "orchestrator-rollout-marker" \
  "${test_dir}/orchestrator-rollout-marker.json"

jq '
  .resource_changes += [{
    "address": "module.unreviewed.random_id.rollout_marker",
    "mode": "managed",
    "type": "random_id",
    "name": "rollout_marker",
    "change": {
      "actions": ["delete", "create"],
      "before": {"id": "old-rollout-marker"},
      "after": {"id": null}
    }
  }]
' "${fixture}" >"${test_dir}/unreviewed-rollout-marker.json"
expect_failure \
  "unreviewed-rollout-marker" \
  "destructive_managed_resources must be empty." \
  "${test_dir}/unreviewed-rollout-marker.json"

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
  ) = [{}]
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
    | select(.address == "google_sql_database_instance.invited_beta")
    | .change.after.settings[0].availability_type
  ) = "ZONAL"
' "${fixture}" >"${test_dir}/cloud-sql-candidate-not-regional.json"
expect_failure \
  "cloud-sql-candidate-not-regional" \
  "invalid_cloud_sql_resources must be empty." \
  "${test_dir}/cloud-sql-candidate-not-regional.json"

jq '
  (
    .resource_changes[]
    | select(
        .address
        == "google_secret_manager_secret.cloud_sql_invited_beta_password"
      )
    | .change.after.secret_id
  ) = "e2b-wrong-password-secret"
' "${fixture}" >"${test_dir}/cloud-sql-candidate-wrong-password-secret.json"
expect_failure \
  "cloud-sql-candidate-wrong-password-secret" \
  "invalid_cloud_sql_resources must be empty." \
  "${test_dir}/cloud-sql-candidate-wrong-password-secret.json"

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
  ) = 30
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
  ) = 1
  |
  (
    .resource_changes[]
    | select(.address == "terraform_data.cloud_sql_connection_budget")
    | .change.after.input.maximum_concurrent_connections
  ) = 19
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
  ) = 44
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
  .resource_changes |= map(
    select(
      .address
      != "module.init.google_secret_manager_secret.postgres_connection_string"
    )
  )
' "${fixture}" >"${test_dir}/cloud-sql-missing-secret-container.json"
expect_failure \
  "cloud-sql-missing-secret-container" \
  "invalid_cloud_sql_resources must be empty." \
  "${test_dir}/cloud-sql-missing-secret-container.json"

jq '
  (
    .resource_changes[]
    | select(
        .address
        == "module.init.google_secret_manager_secret.postgres_connection_string"
      )
  ) as $container
  | .resource_changes += [$container]
' "${fixture}" >"${test_dir}/cloud-sql-duplicate-secret-container.json"
expect_failure \
  "cloud-sql-duplicate-secret-container" \
  "invalid_cloud_sql_resources must be empty." \
  "${test_dir}/cloud-sql-duplicate-secret-container.json"

jq '
  (
    .resource_changes[]
    | select(
        .address
        == "module.init.google_secret_manager_secret.postgres_connection_string"
      )
    | .change.after.name
  ) = "projects/999999999/secrets/e2b-postgres-connection-string"
  | (
      .resource_changes[]
      | select(
          .address
          == "google_secret_manager_secret_version.postgres_connection_string"
        )
      | .change.after.secret
    ) = "projects/999999999/secrets/e2b-postgres-connection-string"
' "${fixture}" >"${test_dir}/cloud-sql-coupled-wrong-numeric-name.json"
expect_failure \
  "cloud-sql-coupled-wrong-numeric-name" \
  "invalid_cloud_sql_resources must be empty." \
  "${test_dir}/cloud-sql-coupled-wrong-numeric-name.json"

jq '
  (
    .resource_changes[]
    | select(
        .address
        == "module.init.google_secret_manager_secret.postgres_connection_string"
      )
    | .change.actions
  ) = ["update"]
' "${fixture}" >"${test_dir}/cloud-sql-secret-container-update.json"
expect_failure \
  "cloud-sql-secret-container-update" \
  "invalid_cloud_sql_resources must be empty." \
  "${test_dir}/cloud-sql-secret-container-update.json"

jq '
  (
    .resource_changes[]
    | select(
        .address
        == "module.init.google_secret_manager_secret.postgres_connection_string"
      )
    | .change.after_unknown
  ) = {
    project: true,
    secret_id: true,
    id: true,
    name: true
  }
' "${fixture}" >"${test_dir}/cloud-sql-secret-container-conflicting-identity.json"
expect_failure \
  "cloud-sql-secret-container-conflicting-identity" \
  "invalid_cloud_sql_resources must be empty." \
  "${test_dir}/cloud-sql-secret-container-conflicting-identity.json"

jq '
  (
    .resource_changes[]
    | select(
        .address
        == "google_secret_manager_secret_version.postgres_connection_string"
      )
    | .change.after_unknown.secret
  ) = true
' "${fixture}" >"${test_dir}/cloud-sql-secret-version-conflicting-target.json"
expect_failure \
  "cloud-sql-secret-version-conflicting-target" \
  "invalid_cloud_sql_resources must be empty." \
  "${test_dir}/cloud-sql-secret-version-conflicting-target.json"

jq '
  (
    .prior_state.values.root_module.child_modules[]
    | select(.address == "module.init")
    | .resources
  ) = []
' "${fixture}" >"${test_dir}/cloud-sql-missing-project-state.json"
expect_failure \
  "cloud-sql-missing-project-state" \
  "invalid_cloud_sql_resources must be empty." \
  "${test_dir}/cloud-sql-missing-project-state.json"

jq '
  (
    .prior_state.values.root_module.child_modules[]
    | select(.address == "module.init")
    | .resources[0]
  ) as $project
  | (
      .prior_state.values.root_module.child_modules[]
      | select(.address == "module.init")
      | .resources
    ) += [$project]
' "${fixture}" >"${test_dir}/cloud-sql-duplicate-project-state.json"
expect_failure \
  "cloud-sql-duplicate-project-state" \
  "invalid_cloud_sql_resources must be empty." \
  "${test_dir}/cloud-sql-duplicate-project-state.json"

jq '
  (
    .prior_state.values.root_module.child_modules[]
    | select(.address == "module.init")
    | .resources[]
    | select(.address == "module.init.data.google_project.current")
    | .values.project_id
  ) = "other-project"
' "${fixture}" >"${test_dir}/cloud-sql-wrong-project-state.json"
expect_failure \
  "cloud-sql-wrong-project-state" \
  "invalid_cloud_sql_resources must be empty." \
  "${test_dir}/cloud-sql-wrong-project-state.json"

jq '
  .resource_changes += [
    {
      address: "module.init.data.google_project.current",
      mode: "data",
      type: "google_project",
      name: "current",
      change: {
        actions: ["update"],
        before: {
          number: "123456789",
          project_id: "operator-canary"
        },
        after: {
          number: "123456789",
          project_id: "operator-canary"
        },
        after_unknown: {}
      }
    }
  ]
' "${fixture}" >"${test_dir}/cloud-sql-unsafe-project-action.json"
expect_failure \
  "cloud-sql-unsafe-project-action" \
  "invalid_cloud_sql_resources must be empty." \
  "${test_dir}/cloud-sql-unsafe-project-action.json"

jq '
  .resource_changes += [
    {
      address: "module.init.data.google_project.current",
      mode: "data",
      type: "google_project",
      name: "current",
      change: {
        actions: ["read"],
        before: {
          number: "123456789",
          project_id: "operator-canary"
        },
        after: {
          number: "123456789",
          project_id: "operator-canary"
        },
        after_unknown: {
          number: true
        }
      }
    }
  ]
' "${fixture}" >"${test_dir}/cloud-sql-conflicting-project-read.json"
expect_failure \
  "cloud-sql-conflicting-project-read" \
  "invalid_cloud_sql_resources must be empty." \
  "${test_dir}/cloud-sql-conflicting-project-read.json"

jq '
  (
    .resource_changes[]
    | select(
        .address
        == "module.init.google_secret_manager_secret.postgres_connection_string"
      )
    | .change.after.project
  ) = "other-project"
' "${fixture}" >"${test_dir}/cloud-sql-secret-container-project.json"
expect_failure \
  "cloud-sql-secret-container-project" \
  "invalid_cloud_sql_resources must be empty." \
  "${test_dir}/cloud-sql-secret-container-project.json"

jq '
  (
    .resource_changes[]
    | select(
        .address
        == "module.init.google_secret_manager_secret.postgres_connection_string"
      )
    | .change.after.secret_id
  ) = "hostile-secret"
' "${fixture}" >"${test_dir}/cloud-sql-secret-container-secret-id.json"
expect_failure \
  "cloud-sql-secret-container-secret-id" \
  "invalid_cloud_sql_resources must be empty." \
  "${test_dir}/cloud-sql-secret-container-secret-id.json"

jq '
  (
    .resource_changes[]
    | select(
        .address
        == "module.init.google_secret_manager_secret.postgres_connection_string"
      )
    | .change.after.id
  ) = "projects/operator-canary/secrets/hostile-secret"
' "${fixture}" >"${test_dir}/cloud-sql-secret-container-id.json"
expect_failure \
  "cloud-sql-secret-container-id" \
  "invalid_cloud_sql_resources must be empty." \
  "${test_dir}/cloud-sql-secret-container-id.json"

jq '
  del(
    .resource_changes[]
    | select(
        .address
        == "module.init.google_secret_manager_secret.postgres_connection_string"
      )
    | .change.after.id
  )
  | (
      .resource_changes[]
      | select(
          .address
          == "module.init.google_secret_manager_secret.postgres_connection_string"
        )
      | .change.after_unknown.id
    ) = true
' "${fixture}" >"${test_dir}/cloud-sql-secret-container-unknown-id.json"
expect_failure \
  "cloud-sql-secret-container-unknown-id" \
  "invalid_cloud_sql_resources must be empty." \
  "${test_dir}/cloud-sql-secret-container-unknown-id.json"

jq '
  (
    .resource_changes[]
    | select(
        .address
        == "google_secret_manager_secret_version.postgres_connection_string"
      )
    | .change.after.secret
  ) = "projects/operator-canary/secrets/e2b-postgres-connection-string"
' "${fixture}" >"${test_dir}/cloud-sql-secret-version-name-mismatch.json"
expect_failure \
  "cloud-sql-secret-version-name-mismatch" \
  "invalid_cloud_sql_resources must be empty." \
  "${test_dir}/cloud-sql-secret-version-name-mismatch.json"

jq '
  del(
    .resource_changes[]
    | select(
        .address
        == "module.init.google_secret_manager_secret.postgres_connection_string"
      )
    | .change.after.name
  )
  | (
      .resource_changes[]
      | select(
          .address
          == "module.init.google_secret_manager_secret.postgres_connection_string"
        )
      | .change.after_unknown.name
    ) = true
' "${fixture}" >"${test_dir}/cloud-sql-secret-container-unknown-name.json"
expect_failure \
  "cloud-sql-secret-container-unknown-name" \
  "invalid_cloud_sql_resources must be empty." \
  "${test_dir}/cloud-sql-secret-container-unknown-name.json"

jq '
  (
    .resource_changes[]
    | select(
        .address
        == "module.init.google_secret_manager_secret.postgres_connection_string"
      )
    | .change.after.name
  ) = null
  | (
      .resource_changes[]
      | select(
          .address
          == "google_secret_manager_secret_version.postgres_connection_string"
        )
      | .change.after.secret
    ) = null
' "${fixture}" >"${test_dir}/cloud-sql-secret-container-null-name.json"
expect_failure \
  "cloud-sql-secret-container-null-name" \
  "invalid_cloud_sql_resources must be empty." \
  "${test_dir}/cloud-sql-secret-container-null-name.json"

jq '
  (
    .resource_changes[]
    | select(
        .address
        == "module.init.google_secret_manager_secret.postgres_connection_string"
      )
    | .change.after.name
  ) = ""
  | (
      .resource_changes[]
      | select(
          .address
          == "google_secret_manager_secret_version.postgres_connection_string"
        )
      | .change.after.secret
    ) = ""
' "${fixture}" >"${test_dir}/cloud-sql-secret-container-empty-name.json"
expect_failure \
  "cloud-sql-secret-container-empty-name" \
  "invalid_cloud_sql_resources must be empty." \
  "${test_dir}/cloud-sql-secret-container-empty-name.json"

jq '
  del(
    .resource_changes[]
    | select(
        .address
        == "google_secret_manager_secret_version.postgres_connection_string"
      )
    | .change.after_sensitive.secret_data
  )
' "${fixture}" >"${test_dir}/cloud-sql-secret-data-not-sensitive.json"
expect_failure \
  "cloud-sql-secret-data-not-sensitive" \
  "invalid_cloud_sql_resources must be empty." \
  "${test_dir}/cloud-sql-secret-data-not-sensitive.json"

jq '
  del(
    .resource_changes[]
    | select(
        .address
        == "google_secret_manager_secret_version.postgres_connection_string"
      )
    | .change.after_unknown.secret_data
  )
' "${fixture}" >"${test_dir}/cloud-sql-secret-data-not-unknown.json"
expect_failure \
  "cloud-sql-secret-data-not-unknown" \
  "invalid_cloud_sql_resources must be empty." \
  "${test_dir}/cloud-sql-secret-data-not-unknown.json"

jq '
  (
    .resource_changes[]
    | select(
        .address
        == "google_secret_manager_secret_version.postgres_connection_string"
      )
    | .change.after.secret_data
  ) = ""
  | del(
      .resource_changes[]
      | select(
          .address
          == "google_secret_manager_secret_version.postgres_connection_string"
        )
      | .change.after_unknown.secret_data
    )
' "${fixture}" >"${test_dir}/cloud-sql-empty-secret-data.json"
expect_failure \
  "cloud-sql-empty-secret-data" \
  "invalid_cloud_sql_resources must be empty." \
  "${test_dir}/cloud-sql-empty-secret-data.json"

jq '
  (
    .resource_changes[]
    | select(.address == "random_password.cloud_sql_operator_canary")
    | .change.after.result
  ) = "fixture-password"
  | del(
      .resource_changes[]
      | select(.address == "random_password.cloud_sql_operator_canary")
      | .change.after_unknown.result
    )
  | (
      .resource_changes[]
      | select(.address == "google_sql_user.operator_canary")
      | .change.after.password
    ) = "fixture-password"
  | del(
      .resource_changes[]
      | select(.address == "google_sql_user.operator_canary")
      | .change.after_unknown.password
    )
  | (
      .resource_changes[]
      | select(.address == "google_sql_database_instance.operator_canary")
      | .change.after.private_ip_address
    ) = "10.0.0.2"
  | del(
      .resource_changes[]
      | select(.address == "google_sql_database_instance.operator_canary")
      | .change.after_unknown.private_ip_address
    )
  | (
      .resource_changes[]
      | select(
          .address
          == "google_secret_manager_secret_version.postgres_connection_string"
        )
      | .change.after.secret_data
    ) = "postgresql://e2b:fixture-password@10.0.0.2:5432/e2b?sslmode=require"
  | del(
      .resource_changes[]
      | select(
          .address
          == "google_secret_manager_secret_version.postgres_connection_string"
        )
      | .change.after_unknown.secret_data
    )
' "${fixture}" >"${test_dir}/cloud-sql-concrete-connection-uri.json"
expect_success \
  "cloud-sql-concrete-connection-uri" \
  "${test_dir}/cloud-sql-concrete-connection-uri.json"

jq '
  (
    .resource_changes[]
    | select(
        .address
        == "google_secret_manager_secret_version.postgres_connection_string"
      )
    | .change.after_unknown.secret_data
  ) = true
' "${test_dir}/cloud-sql-concrete-connection-uri.json" \
  >"${test_dir}/cloud-sql-conflicting-concrete-unknown-uri.json"
expect_failure \
  "cloud-sql-conflicting-concrete-unknown-uri" \
  "invalid_cloud_sql_resources must be empty." \
  "${test_dir}/cloud-sql-conflicting-concrete-unknown-uri.json"

jq '
  (
    .resource_changes[]
    | select(
        .address
        == "google_secret_manager_secret_version.postgres_connection_string"
      )
    | .change.after.secret_data
  ) = "postgresql://e2b:wrong@10.0.0.2:5432/e2b?sslmode=require"
' "${test_dir}/cloud-sql-concrete-connection-uri.json" \
  >"${test_dir}/cloud-sql-concrete-connection-uri-mismatch.json"
expect_failure \
  "cloud-sql-concrete-connection-uri-mismatch" \
  "invalid_cloud_sql_resources must be empty." \
  "${test_dir}/cloud-sql-concrete-connection-uri-mismatch.json"

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
  .resource_changes |= map(
    select(.address != "google_sql_database_instance.invited_beta")
  )
' "${fixture}" >"${test_dir}/missing-cloud-sql-candidate.json"
expect_failure \
  "missing-cloud-sql-candidate" \
  "missing_or_duplicate_cloud_sql_resources must be empty." \
  "${test_dir}/missing-cloud-sql-candidate.json"

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
    .planned_values.root_module
    | recurse(.child_modules[]?)
    | .resources?
  ) |= map(
    select(.type != "google_artifact_registry_docker_image")
  )
' "${fixture}" >"${test_dir}/prior-state-core-images-including-jobless-clickhouse.json"
expect_success \
  "prior-state-core-images-including-jobless-clickhouse" \
  "${test_dir}/prior-state-core-images-including-jobless-clickhouse.json"

jq '
  (
    .planned_values.root_module.child_modules[0].resources
  ) += [
    (
      .planned_values.root_module.child_modules[0].resources[]
      | select(
          .address
          == "module.nomad.data.google_artifact_registry_docker_image.api_image"
        )
      | .address
        = "module.k8s_apps.data.google_artifact_registry_docker_image.api_image"
    )
  ]
' "${fixture}" >"${test_dir}/sibling-module-core-image-name.json"
expect_success \
  "sibling-module-core-image-name" \
  "${test_dir}/sibling-module-core-image-name.json"

jq '
  (
    .planned_values.root_module
    | recurse(.child_modules[]?)
    | .resources?
  ) |= map(
    select(
      .address
      != "module.nomad.data.google_artifact_registry_docker_image.api_image"
    )
  )
  | (
      .prior_state.values.root_module
      | recurse(.child_modules[]?)
      | .resources[]?
      | select(
          .address
          == "module.nomad.data.google_artifact_registry_docker_image.api_image"
        )
      | .values
    ) |= (
      .name = "projects/monad-code/locations/us-east4/repositories/e2b-core/dockerImages/api@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
      | .self_link = "us-east4-docker.pkg.dev/monad-code/e2b-core/api@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
    )
' "${fixture}" >"${test_dir}/prior-state-core-image-coupled-tamper.json"
expect_failure \
  "prior-state-core-image-coupled-tamper" \
  "invalid_core_images must be empty." \
  "${test_dir}/prior-state-core-image-coupled-tamper.json"

jq '
  (
    .planned_values.root_module
    | recurse(.child_modules[]?)
    | .resources[]?
    | select(
        .address
        == "module.nomad.data.google_artifact_registry_docker_image.api_image"
      )
    | .values
  ) |= (
    .name = "projects/monad-code/locations/us-east4/repositories/e2b-core/dockerImages/api@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
    | .self_link = "us-east4-docker.pkg.dev/monad-code/e2b-core/api@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
  )
' "${fixture}" >"${test_dir}/planned-core-image-coupled-tamper.json"
expect_failure \
  "planned-core-image-coupled-tamper" \
  "invalid_core_images must be empty." \
  "${test_dir}/planned-core-image-coupled-tamper.json"

jq '
  (
    .planned_values.root_module.child_modules[0].resources
  ) += [
    (
      .planned_values.root_module.child_modules[0].resources[]
      | select(
          .address
          == "module.nomad.data.google_artifact_registry_docker_image.api_image"
        )
    )
  ]
' "${fixture}" >"${test_dir}/duplicate-planned-core-image.json"
expect_failure \
  "duplicate-planned-core-image" \
  "missing_or_duplicate_core_images must be empty." \
  "${test_dir}/duplicate-planned-core-image.json"

jq '
  (
    .prior_state.values.root_module.child_modules[]
    | select(.address == "module.nomad")
    | .resources
  ) += [
    (
      .prior_state.values.root_module.child_modules[]
      | select(.address == "module.nomad")
      | .resources[]
      | select(
          .address
          == "module.nomad.data.google_artifact_registry_docker_image.api_image"
        )
    )
  ]
' "${fixture}" >"${test_dir}/duplicate-prior-core-image.json"
expect_failure \
  "duplicate-prior-core-image" \
  "missing_or_duplicate_core_images must be empty." \
  "${test_dir}/duplicate-prior-core-image.json"

jq '
  .resource_changes += [
    {
      address: "module.nomad.data.google_artifact_registry_docker_image.api_image",
      mode: "data",
      type: "google_artifact_registry_docker_image",
      name: "api_image",
      change: {
        actions: ["read"],
        before: null,
        after: {},
        after_unknown: {}
      }
    }
  ]
' "${fixture}" >"${test_dir}/core-image-read-change.json"
expect_failure \
  "core-image-read-change" \
  "missing_or_duplicate_core_images must be empty." \
  "${test_dir}/core-image-read-change.json"

jq '
  (
    .planned_values.root_module
    | recurse(.child_modules[]?)
    | .resources[]?
    | select(
        .address
        == "module.nomad.data.google_artifact_registry_docker_image.api_image"
      )
    | .values
  ) |= del(.self_link)
' "${fixture}" >"${test_dir}/partial-core-image-identity.json"
expect_failure \
  "partial-core-image-identity" \
  "invalid_core_images must be empty." \
  "${test_dir}/partial-core-image-identity.json"

jq '
  (
    .planned_values.root_module
    | recurse(.child_modules[]?)
    | .resources[]?
    | select(
        .address
        == "module.nomad.data.google_artifact_registry_docker_image.api_image"
      )
    | .values
  ) = {
    image_name: "api:latest",
    location: "us-east4",
    repository_id: "e2b-core"
  }
  | .proposed_unknown.root_module.child_modules = [
      {
        address: "module.nomad",
        resources: [
          {
            address: "module.nomad.data.google_artifact_registry_docker_image.api_image",
            mode: "data",
            type: "google_artifact_registry_docker_image",
            name: "api_image",
            values: {
              name: true,
              self_link: true
            }
          }
        ]
      }
    ]
' "${fixture}" >"${test_dir}/deferred-unknown-core-image-identity.json"
expect_failure \
  "deferred-unknown-core-image-identity" \
  "invalid_core_images must be empty." \
  "${test_dir}/deferred-unknown-core-image-identity.json"

jq '
  (
    .planned_values.root_module
    | recurse(.child_modules[]?)
    | .resources[]?
    | select(
        .address
        == "module.nomad.data.google_artifact_registry_docker_image.api_image"
      )
    | .mode
  ) = "managed"
' "${fixture}" >"${test_dir}/wrong-core-image-mode.json"
expect_failure \
  "wrong-core-image-mode" \
  "invalid_core_images must be empty." \
  "${test_dir}/wrong-core-image-mode.json"

jq '
  (
    .planned_values.root_module
    | recurse(.child_modules[]?)
    | .resources[]?
    | select(
        .address
        == "module.nomad.data.google_artifact_registry_docker_image.api_image"
      )
    | .type
  ) = "google_storage_bucket_object"
' "${fixture}" >"${test_dir}/wrong-core-image-type.json"
expect_failure \
  "wrong-core-image-type" \
  "invalid_core_images must be empty." \
  "${test_dir}/wrong-core-image-type.json"

jq '
  (
    .planned_values.root_module
    | recurse(.child_modules[]?)
    | .resources[]?
    | select(
        .address
        == "module.nomad.data.google_artifact_registry_docker_image.api_image"
      )
    | .address
  ) = "module.nomad.data.google_artifact_registry_docker_image.unreviewed"
  | (
      .prior_state.values.root_module.child_modules[]
      | select(.address == "module.nomad")
      | .resources
    ) |= map(
      select(
        .address
        != "module.nomad.data.google_artifact_registry_docker_image.api_image"
      )
    )
' "${fixture}" >"${test_dir}/wrong-core-image-address.json"
expect_failure \
  "wrong-core-image-address" \
  "missing_or_duplicate_core_images must be empty." \
  "${test_dir}/wrong-core-image-address.json"

jq '
  (
    .configuration.root_module.module_calls.nomad.module.resources[]
    | select(
        .address
        == "data.google_artifact_registry_docker_image.api_image"
      )
    | .expressions.location.references
  ) = ["var.unreviewed_region"]
' "${fixture}" >"${test_dir}/core-image-config-wiring-drift.json"
expect_failure \
  "core-image-config-wiring-drift" \
  "invalid_core_images must be empty." \
  "${test_dir}/core-image-config-wiring-drift.json"

jq '
  (
    .configuration.root_module.module_calls.nomad.module.resources
  ) += [
    (
      .configuration.root_module.module_calls.nomad.module.resources[]
      | select(
          .address
          == "data.google_artifact_registry_docker_image.api_image"
        )
    )
  ]
' "${fixture}" >"${test_dir}/duplicate-core-image-config.json"
expect_failure \
  "duplicate-core-image-config" \
  "invalid_core_images must be empty." \
  "${test_dir}/duplicate-core-image-config.json"

jq '
  .configuration.root_module.module_calls.nomad.module.resources |= map(
    select(
      .address
      != "data.google_artifact_registry_docker_image.api_image"
    )
  )
' "${fixture}" >"${test_dir}/missing-core-image-config.json"
expect_failure \
  "missing-core-image-config" \
  "invalid_core_images must be empty." \
  "${test_dir}/missing-core-image-config.json"

jq '
  .configuration.provider_config["module.nomad:google"]
    = .configuration.provider_config.google
  | del(.configuration.provider_config.google)
  | (
      .configuration.root_module.module_calls.nomad.module.resources[]
      | select(
          .type == "google_artifact_registry_docker_image"
          or .type == "google_storage_bucket_object"
        )
      | .provider_config_key
    ) = "module.nomad:google"
' "${fixture}" >"${test_dir}/opaque-core-image-provider-key.json"
expect_success \
  "opaque-core-image-provider-key" \
  "${test_dir}/opaque-core-image-provider-key.json"

jq '
  .configuration.provider_config.google.full_name
    = "registry.terraform.io/unreviewed/google"
' "${fixture}" >"${test_dir}/malicious-core-image-provider.json"
expect_failure \
  "malicious-core-image-provider" \
  "invalid_core_images must be empty." \
  "${test_dir}/malicious-core-image-provider.json"

jq '
  .configuration.provider_config.google.alias = "unreviewed"
' "${fixture}" >"${test_dir}/aliased-core-image-provider.json"
expect_failure \
  "aliased-core-image-provider" \
  "invalid_core_images must be empty." \
  "${test_dir}/aliased-core-image-provider.json"

jq '
  .configuration.provider_config.google.expressions.project.references
    = ["var.unreviewed_project"]
' "${fixture}" >"${test_dir}/core-image-provider-wiring-drift.json"
expect_failure \
  "core-image-provider-wiring-drift" \
  "invalid_core_images must be empty." \
  "${test_dir}/core-image-provider-wiring-drift.json"

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
    .resource_changes[]
    | select(.address == "module.nomad.module.api.nomad_job.api")
    | .change.after.jobspec
  ) = "job \"api\" {\n  datacenters = [\"dc1\"]\n  meta {\n    reviewed_images = <<-EOT\nimage = \"us-east4-docker.pkg.dev/monad-code/e2b-core/api@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\"\nimage = \"us-east4-docker.pkg.dev/monad-code/e2b-core/db-migrator@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\"\nEOT\n  }\n  group \"api\" {\n    task \"api\" {\n      driver = \"docker\"\n      config {\n        image = trimspace(<<-EOT\nmalicious.example.invalid/api@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb\nEOT\n        )\n      }\n    }\n    task \"db-migrator\" {\n      driver = \"docker\"\n      config {\n        image = \"us-east4-docker.pkg.dev/monad-code/e2b-core/db-migrator@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\"\n      }\n    }\n  }\n}"
' "${fixture}" >"${test_dir}/core-job-heredoc-decoy.json"
expect_failure \
  "core-job-heredoc-decoy" \
  "invalid_core_jobs must be empty." \
  "${test_dir}/core-job-heredoc-decoy.json"

jq '
  .resource_changes |= map(
    select(.address != "module.nomad.module.api.nomad_job.api")
  )
' "${fixture}" >"${test_dir}/missing-core-job.json"
expect_failure \
  "missing-core-job" \
  "missing_or_duplicate_core_jobs must be empty." \
  "${test_dir}/missing-core-job.json"

jq '
  .resource_changes += [
    (
      .resource_changes[]
      | select(.address == "module.nomad.module.api.nomad_job.api")
    )
  ]
' "${fixture}" >"${test_dir}/duplicate-core-job.json"
expect_failure \
  "duplicate-core-job" \
  "missing_or_duplicate_core_jobs must be empty." \
  "${test_dir}/duplicate-core-job.json"

jq '
  (
    .resource_changes[]
    | select(.address == "module.nomad.module.api.nomad_job.api")
    | .change.actions
  ) = ["read"]
' "${fixture}" >"${test_dir}/unsafe-core-job-action.json"
expect_failure \
  "unsafe-core-job-action" \
  "invalid_core_jobs must be empty." \
  "${test_dir}/unsafe-core-job-action.json"

jq '
  (
    .resource_changes[]
    | select(.address == "module.nomad.module.api.nomad_job.api")
    | .change.after_unknown.jobspec
  ) = true
' "${fixture}" >"${test_dir}/unknown-core-job-jobspec.json"
expect_failure \
  "unknown-core-job-jobspec" \
  "invalid_core_jobs must be empty." \
  "${test_dir}/unknown-core-job-jobspec.json"

jq '
  (
    .resource_changes[]
    | select(
        .address == "module.nomad.nomad_job.docker_reverse_proxy"
      )
    | .change.after.jobspec
  ) |= sub(
    "        image = \"us-east4-docker.pkg.dev/monad-code/e2b-core/docker-reverse-proxy@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\"\n";
    ""
  )
' "${fixture}" >"${test_dir}/missing-core-job-image.json"
expect_failure \
  "missing-core-job-image" \
  "invalid_core_jobs must be empty." \
  "${test_dir}/missing-core-job-image.json"

jq '
  (
    .resource_changes[]
    | select(
        .address == "module.nomad.nomad_job.docker_reverse_proxy"
      )
    | .change.after.jobspec
  ) |= sub(
    "image = \"us-east4-docker.pkg.dev/monad-code/e2b-core/docker-reverse-proxy@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\"";
    "image = 42"
  )
' "${fixture}" >"${test_dir}/non-string-core-job-image.json"
expect_failure \
  "non-string-core-job-image" \
  "invalid_core_jobs must be empty." \
  "${test_dir}/non-string-core-job-image.json"

jq '
  (
    .resource_changes[]
    | select(.address == "module.nomad.module.api.nomad_job.api")
    | .change.after.jobspec
  ) |= sub(
    "us-east4-docker.pkg.dev/monad-code/e2b-core/api@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa";
    "us-east4-docker.pkg.dev/monad-code/e2b-core/api:latest"
  )
' "${fixture}" >"${test_dir}/tagged-core-job-image.json"
expect_failure \
  "tagged-core-job-image" \
  "invalid_core_jobs must be empty." \
  "${test_dir}/tagged-core-job-image.json"

jq '
  (
    .resource_changes[]
    | select(.address == "module.nomad.module.api.nomad_job.api")
    | .change.after.jobspec
  ) |= sub(
    "\n}$";
    "\n  group \"unreviewed\" {\n    task \"unreviewed\" {\n      driver = \"raw_exec\"\n      config {\n        command = \"/bin/true\"\n      }\n    }\n  }\n}"
  )
' "${fixture}" >"${test_dir}/non-docker-core-job-task.json"
expect_failure \
  "non-docker-core-job-task" \
  "invalid_core_jobs must be empty." \
  "${test_dir}/non-docker-core-job-task.json"

jq '
  (
    .resource_changes[]
    | select(.address == "module.nomad.module.api.nomad_job.api")
    | .change.after.jobspec
  ) |= sub(
    "  group \"api\" [{]\n";
    "  group \"api\" {\n    service {\n      name = \"api\"\n      provider = \"nomad\"\n      connect {\n        sidecar_service {}\n        sidecar_task {\n          driver = \"docker\"\n          config {\n            image = \"malicious.example.invalid/connect@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb\"\n          }\n        }\n      }\n    }\n"
  )
' "${fixture}" >"${test_dir}/connect-sidecar-core-job-image.json"
expect_failure \
  "connect-sidecar-core-job-image" \
  "invalid_core_jobs must be empty." \
  "${test_dir}/connect-sidecar-core-job-image.json"

jq '
  (
    .resource_changes[]
    | select(.address == "module.nomad.module.api.nomad_job.api")
    | .change.after.jobspec
  ) |= sub(
    "      driver = \"docker\"\n      config [{]";
    "      driver = \"docker\"\n      service {\n        name = \"api-task\"\n        provider = \"nomad\"\n        connect {\n          sidecar_service {}\n          sidecar_task {\n            driver = \"docker\"\n            config {\n              image = \"malicious.example.invalid/task-connect@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb\"\n            }\n          }\n        }\n      }\n      config {"
  )
' "${fixture}" >"${test_dir}/task-connect-sidecar-core-job-image.json"
expect_failure \
  "task-connect-sidecar-core-job-image" \
  "invalid_core_jobs must be empty." \
  "${test_dir}/task-connect-sidecar-core-job-image.json"

jq '
  (
    .resource_changes[]
    | select(.address == "module.nomad.module.api.nomad_job.api")
    | .change.after.jobspec
  ) |= sub(
    "  group \"api\" [{]\n";
    "  group \"api\" {\n    service {\n      name = \"api\"\n      provider = \"nomad\"\n    }\n"
  )
' "${fixture}" >"${test_dir}/service-without-connect-core-job.json"
expect_success \
  "service-without-connect-core-job" \
  "${test_dir}/service-without-connect-core-job.json"

jq '
  (
    .resource_changes[]
    | select(.address == "module.nomad.module.api.nomad_job.api")
    | .change.after.jobspec
  ) |= sub(
    "\n}$";
    "\n  group \"extra\" {\n    task \"extra\" {\n      driver = \"docker\"\n      config {\n        image = \"malicious.example.invalid/extra@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb\"\n      }\n    }\n  }\n}"
  )
' "${fixture}" >"${test_dir}/extra-core-job-image.json"
expect_failure \
  "extra-core-job-image" \
  "invalid_core_jobs must be empty." \
  "${test_dir}/extra-core-job-image.json"

jq '
  (
    .resource_changes[]
    | select(.address == "module.nomad.module.api.nomad_job.api")
    | .change.after.jobspec
  ) |= sub(
    "\n}$";
    "\n  group \"duplicate\" {\n    task \"duplicate\" {\n      driver = \"docker\"\n      config {\n        image = \"us-east4-docker.pkg.dev/monad-code/e2b-core/api@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\"\n      }\n    }\n  }\n}"
  )
' "${fixture}" >"${test_dir}/duplicate-core-job-image.json"
expect_failure \
  "duplicate-core-job-image" \
  "invalid_core_jobs must be empty." \
  "${test_dir}/duplicate-core-job-image.json"

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
    select(.type != "google_storage_bucket_object")
  )
' "${fixture}" >"${test_dir}/prior-state-job-binary-objects.json"
expect_success \
  "prior-state-job-binary-objects" \
  "${test_dir}/prior-state-job-binary-objects.json"

jq '
  (
    .planned_values.root_module.child_modules[].resources
  ) |= map(
    select(
      .address
      != "module.nomad.data.google_storage_bucket_object.filestore_cleanup"
    )
  )
  | (
      .prior_state.values.root_module.child_modules[].resources
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
    .planned_values.root_module.child_modules[].resources
  ) |= map(
    select(.type != "google_storage_bucket_object")
  )
  | (
      .prior_state.values.root_module
      | recurse(.child_modules[]?)
      | .resources[]?
      | select(
          .address
          == "module.nomad.data.google_storage_bucket_object.template_manager"
        )
      | .values.generation
    ) = 9999
' "${fixture}" >"${test_dir}/prior-state-job-binary-generation-drift.json"
expect_failure \
  "prior-state-job-binary-generation-drift" \
  "invalid_job_binary_objects must be empty." \
  "${test_dir}/prior-state-job-binary-generation-drift.json"

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
  (
    .resource_changes[]
    | select(
        .address
        == "module.nomad.module.template_manager.nomad_job.template_manager"
      )
    | .change
  ) |= (
    .after.jobspec = null
    | .after_unknown.jobspec = true
  )
' "${fixture}" >"${test_dir}/unknown-template-manager-jobspec.json"
expect_success \
  "unknown-template-manager-jobspec" \
  "${test_dir}/unknown-template-manager-jobspec.json"

jq '
  (
    .resource_changes[]
    | select(
        .address
        == "module.nomad.module.template_manager.nomad_job.template_manager"
      )
    | .change
  ) |= (
    .after.jobspec = null
    | .after_unknown.jobspec = true
  )
  | (
      .configuration.root_module.module_calls.nomad.module
      .module_calls.template_manager.expressions.artifact_source.references
    ) = ["local.unreviewed_artifact_source"]
' "${fixture}" >"${test_dir}/unknown-template-manager-wiring-drift.json"
expect_failure \
  "unknown-template-manager-wiring-drift" \
  "invalid_job_binary_jobs must be empty." \
  "${test_dir}/unknown-template-manager-wiring-drift.json"

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
  | (
      .prior_state.values.root_module.child_modules[].resources
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

jq '.max_automated_rollout_surge_per_pool.server = 2' \
  "${policy}" >"${test_dir}/server-surge-policy-drift.json"
expect_failure \
  "server-surge-policy-drift" \
  "Workload topology policy is invalid or differs from reviewed quota limits" \
  "${fixture}" \
  "${test_dir}/server-surge-policy-drift.json"

jq '.server_control_plane_rollout.min_ready_sec = 30' \
  "${policy}" >"${test_dir}/server-ready-policy-drift.json"
expect_failure \
  "server-ready-policy-drift" \
  "Workload topology policy is invalid or differs from reviewed quota limits" \
  "${fixture}" \
  "${test_dir}/server-ready-policy-drift.json"

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
