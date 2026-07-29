#!/usr/bin/env bash
set -euo pipefail

plan_path="${1:?usage: select-workload-quota-mode.sh PLAN_PATH TERRAFORM_BIN POLICY_PATH}"
terraform_bin="${2:?usage: select-workload-quota-mode.sh PLAN_PATH TERRAFORM_BIN POLICY_PATH}"
policy_path="${3:?usage: select-workload-quota-mode.sh PLAN_PATH TERRAFORM_BIN POLICY_PATH}"

command -v jq >/dev/null 2>&1 || {
  printf 'jq is required to select workload quota mode.\n' >&2
  exit 1
}

[[ -f "${plan_path}" && ! -L "${plan_path}" ]] || {
  printf 'Reviewed workload plan must be a regular, non-symlink file: %s\n' \
    "${plan_path}" >&2
  exit 1
}
[[ -f "${policy_path}" && ! -L "${policy_path}" ]] || {
  printf 'Workload topology policy must be a regular, non-symlink file: %s\n' \
    "${policy_path}" >&2
  exit 1
}
if [[ ! -x "${terraform_bin}" ]] \
  && ! command -v "${terraform_bin}" >/dev/null 2>&1; then
  printf 'Terraform is not installed or executable: %s\n' "${terraform_bin}" >&2
  exit 1
fi

plan_json="$("${terraform_bin}" show -json "${plan_path}")" || {
  printf 'Unable to inspect reviewed workload plan: %s\n' "${plan_path}" >&2
  exit 1
}
policy_json="$(jq -ce . "${policy_path}")" || {
  printf 'Workload topology policy is not valid JSON: %s\n' "${policy_path}" >&2
  exit 1
}

selection="$(
  jq -cer \
    --argjson expected "${policy_json}" '
      def mig_role:
        if (
          .type == "google_compute_region_instance_group_manager"
          and .address
            == "module.cluster.google_compute_region_instance_group_manager.server_pool"
        ) then
          "server"
        elif (
          .type == "google_compute_instance_group_manager"
          and .address
            == "module.cluster.google_compute_instance_group_manager.api_pool"
        ) then
          "api"
        elif (
          .type == "google_compute_instance_group_manager"
          and .address
            == "module.cluster.google_compute_instance_group_manager.clickhouse_pool"
        ) then
          "clickhouse"
        elif (
          .type == "google_compute_instance_group_manager"
          and .address
            == "module.cluster.google_compute_instance_group_manager.loki_pool"
        ) then
          "loki"
        elif (
          .type == "google_compute_region_instance_group_manager"
          and (
            .address
            | test(
                "^module\\.cluster\\.module\\.build_cluster\\[\"[^\"]+\"\\]\\.google_compute_region_instance_group_manager\\.pool$"
              )
          )
        ) then
          "build"
        elif (
          .type == "google_compute_region_instance_group_manager"
          and (
            .address
            | test(
                "^module\\.cluster\\.module\\.client_cluster\\[\"[^\"]+\"\\]\\.google_compute_region_instance_group_manager\\.pool$"
              )
          )
        ) then
          "client"
        else
          null
        end;

      def autoscaler_address($address):
        $address
        | sub(
            "\\.google_compute_region_instance_group_manager\\.pool$";
            ".google_compute_region_autoscaler.autoscaler[0]"
          );

      def capacity($resource; $changes; $side):
        if ($resource.change[$side].target_size | type) == "number" then
          $resource.change[$side].target_size
        else
          autoscaler_address($resource.address) as $autoscaler
          | (
              [
                $changes[]
                | select(.address == $autoscaler)
                | .change[$side].autoscaling_policy[0].max_replicas
                | select(type == "number")
              ][0] // null
            )
        end;

      [
        .resource_changes[]?
        | select(.mode == "managed")
      ] as $changes
      | [
          $changes[]
          | select(
              .type == "google_compute_instance_group_manager"
              or .type == "google_compute_region_instance_group_manager"
            )
          | . + {role: mig_role}
        ] as $migs
      | [
          $changes[]
          | select(
              .type == "google_compute_instance"
              or .type == "google_compute_disk"
              or .type == "google_compute_region_disk"
              or .type == "google_compute_address"
              or .type == "google_compute_region_address"
              or .type == "google_compute_region_autoscaler"
            )
          | select(
              .change.actions != ["no-op"]
              and .change.actions != ["read"]
            )
          | select(
              .type != "google_compute_region_autoscaler"
              or (
                .address
                | test(
                    "^module\\.cluster\\.module\\.(build|client)_cluster\\[\"[^\"]+\"\\]\\.google_compute_region_autoscaler\\.autoscaler\\[0\\]$"
                  )
              )
            )
          | {
              address,
              type,
              actions: .change.actions
            }
        ] as $unexpected_quota_mutations
      | [
          $expected.expected_role_max_instances
          | to_entries[]
          | . as $expected_role
          | [
              $migs[]
              | select(.role == $expected_role.key)
            ] as $matches
          | {
              role: $expected_role.key,
              expected: $expected_role.value,
              count: ($matches | length),
              actions: ($matches[0].change.actions // null),
              before: (
                if ($matches | length) == 1
                then capacity($matches[0]; $changes; "before")
                else null
                end
              ),
              after: (
                if ($matches | length) == 1
                then capacity($matches[0]; $changes; "after")
                else null
                end
              )
            }
        ] as $roles
      | {
          unexpected_quota_mutations: $unexpected_quota_mutations,
          invalid_roles: [
            $roles[]
            | select(
                .count != 1
                or (.after | type) != "number"
                or .after != .expected
                or (
                  (.actions | type) != "array"
                  or (
                    .actions != ["create"]
                    and .actions != ["no-op"]
                    and .actions != ["update"]
                  )
                )
              )
          ],
          roles: $roles
        }
      | (
          all(
            .roles[];
            if .expected > 0
            then (
              .before == .expected
              and (.actions == ["no-op"] or .actions == ["update"])
            )
            else true
            end
          )
        ) as $fully_applied
      | if (.invalid_roles | length) != 0 then
          error(
            "reviewed cluster fleet differs from quota policy: "
            + (.invalid_roles | tojson)
          )
        elif (
          $fully_applied
          and (.unexpected_quota_mutations | length) != 0
        ) then
          error(
            "unexpected direct quota-bearing mutation against applied fleet: "
            + (.unexpected_quota_mutations | tojson)
          )
        elif $fully_applied then
          "post-cluster"
        else
          "bootstrap"
        end
    ' <<<"${plan_json}"
)" || {
  printf 'Unable to select a safe workload quota mode from the reviewed plan.\n' >&2
  exit 1
}

printf '%s\n' "${selection}"
