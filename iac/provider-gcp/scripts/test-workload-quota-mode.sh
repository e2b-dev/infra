#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
provider_root="$(cd "${script_dir}/.." && pwd)"
fixture="${script_dir}/testdata/minimal-workload-plan.json"
policy="${provider_root}/topology/minimal-workload-policy.json"
fake_terraform="${script_dir}/testdata/fake-terraform.sh"
selector="${script_dir}/select-workload-quota-mode.sh"
test_dir="$(mktemp -d)"
trap 'rm -rf "${test_dir}"' EXIT

expect_mode() {
  local description="$1"
  local expected="$2"
  local plan="$3"
  local actual

  actual="$("${selector}" "${plan}" "${fake_terraform}" "${policy}")" || {
    printf 'expected quota mode selection to pass: %s\n' "${description}" >&2
    exit 1
  }
  if [[ "${actual}" != "${expected}" ]]; then
    printf 'unexpected quota mode for %s: expected %s, got %s\n' \
      "${description}" "${expected}" "${actual}" >&2
    exit 1
  fi
}

expect_rejected() {
  local description="$1"
  local plan="$2"

  if "${selector}" "${plan}" "${fake_terraform}" "${policy}" \
    >"${test_dir}/stdout" 2>"${test_dir}/stderr"; then
    printf 'expected quota mode selection to reject: %s\n' "${description}" >&2
    exit 1
  fi
}

cp "${fixture}" "${test_dir}/initial-create.json"
expect_mode \
  "initial cluster creation retains full-peak admission" \
  bootstrap \
  "${test_dir}/initial-create.json"

jq '
  .resource_changes += [
    {
      address: "module.cluster.google_compute_disk.initial-create",
      mode: "managed",
      type: "google_compute_disk",
      change: {
        actions: ["create"],
        before: null,
        after: {
          size: 10,
          type: "pd-ssd"
        },
        after_unknown: {}
      }
    }
  ]
' "${fixture}" >"${test_dir}/initial-direct-create.json"
expect_mode \
  "fresh fleet direct creates cannot weaken bootstrap admission" \
  bootstrap \
  "${test_dir}/initial-direct-create.json"

jq '
  .resource_changes |= map(
    if (
      .type == "google_compute_instance_group_manager"
      or .type == "google_compute_region_instance_group_manager"
      or .type == "google_compute_address"
    )
    then .change = (
      .change
      | .before = .after
      | .actions = ["no-op"]
    )
    else .
    end
  )
' "${fixture}" >"${test_dir}/applied-no-op.json"
expect_mode \
  "fully applied no-op cluster uses only peak-minus-base reserve" \
  post-cluster \
  "${test_dir}/applied-no-op.json"

jq '
  .resource_changes |= map(
    if (
      .type == "google_compute_instance_group_manager"
      or .type == "google_compute_region_instance_group_manager"
      or .type == "google_compute_address"
    )
    then .change = (
      .change
      | .before = .after
      | .actions = (
          if (
            .after.target_size == 1
            and (
              .after.update_policy[0].max_surge_fixed // 0
            ) == 0
          )
          then ["update"]
          else ["no-op"]
          end
        )
    )
    else .
    end
  )
' "${fixture}" >"${test_dir}/applied-update.json"
expect_mode \
  "applied in-place worker update uses post-cluster reserve" \
  post-cluster \
  "${test_dir}/applied-update.json"

jq '
  .resource_changes |= map(
    if (
      .type == "google_compute_instance_group_manager"
      or .type == "google_compute_region_instance_group_manager"
      or .type == "google_compute_address"
    )
    then .change = (
      .change
      | .before = .after
      | .actions = (
          if (
            .address
            == "module.cluster.module.client_cluster[\"default\"].google_compute_region_instance_group_manager.pool"
          )
          then ["update"]
          else ["no-op"]
          end
        )
    )
    elif (
      .address
      == "module.cluster.module.client_cluster[\"default\"].google_compute_instance_template.template"
    )
    then .change = (
      .change
      | .before = .after
      | .before.name = "e2b-orch-client-old"
      | .after.name = "e2b-orch-client-new"
      | .actions = ["create", "delete"]
    )
    else .
    end
  )
' "${fixture}" >"${test_dir}/safe-create-before-destroy.json"
expect_mode \
  "reviewed client-template create-before-destroy rollout uses post-cluster reserve" \
  post-cluster \
  "${test_dir}/safe-create-before-destroy.json"

jq '
  .resource_changes |= map(
    if (
      .type == "google_compute_instance_group_manager"
      or .type == "google_compute_region_instance_group_manager"
      or .type == "google_compute_address"
    )
    then .change = (
      .change
      | .before = .after
      | .actions = ["no-op"]
    )
    else .
    end
  )
  | .resource_changes += [
      {
        address: "module.cluster.google_compute_instance.overcommit",
        mode: "managed",
        type: "google_compute_instance",
        change: {
          actions: ["create"],
          before: null,
          after: {
            machine_type: "e2-standard-32"
          },
          after_unknown: {}
        }
      }
    ]
' "${fixture}" >"${test_dir}/true-overcommit.json"
expect_rejected \
  "applied-fleet direct quota overcommit cannot obtain post-cluster admission" \
  "${test_dir}/true-overcommit.json"

printf 'Workload quota mode selection tests passed.\n'
