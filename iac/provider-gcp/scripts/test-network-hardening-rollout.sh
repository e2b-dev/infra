#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
provider_root="$(cd "${script_dir}/.." && pwd)"
repo_root="$(cd "${provider_root}/../.." && pwd)"
test_dir="$(mktemp -d)"
trap 'rm -rf "${test_dir}"' EXIT

fake_terraform="${test_dir}/terraform"
# These are literal lines in the generated fixture, not parent-shell expansions.
# shellcheck disable=SC2016
printf '%s\n' \
  '#!/usr/bin/env bash' \
  'set -euo pipefail' \
  '[[ "${1:-}" == "show" && "${2:-}" == "-json" ]] || exit 2' \
  'cat "${3:?missing plan}"' \
  >"${fake_terraform}"
chmod 0755 "${fake_terraform}"

expect_fail() {
  local description="$1"
  shift
  if "$@" >"${test_dir}/stdout" 2>"${test_dir}/stderr"; then
    printf 'expected failure: %s\n' "${description}" >&2
    exit 1
  fi
}

cluster_source="${provider_root}/nomad-cluster/main.tf"
runbook="${repo_root}/docs/MONAD_GCP_NETWORK_HARDENING.md"
grep -F 'resource "terraform_data" "network_hardening_rollout_completion"' \
  "${cluster_source}" >/dev/null
grep -F 'command = "\"${abspath("${path.module}/../scripts/wait-network-hardening-stage.sh")}\""' \
  "${cluster_source}" >/dev/null
grep -F 'DOMAIN_NAME                     = var.domain_name' \
  "${cluster_source}" >/dev/null
grep -F 'depends_on = [terraform_data.network_hardening_rollout_completion]' \
  "${cluster_source}" >/dev/null
if grep -R -n 'terraform_data\.network_hardening_rollout_stage' \
  "${provider_root}/nomad-cluster" >/dev/null; then
  printf 'stage marker must not remain upstream of a template, MIG, or firewall\n' >&2
  exit 1
fi

for recovery_target in \
  workload-cluster-recover-lease \
  workload-cluster-plan \
  workload-cluster-apply; do
  grep -F "mise exec -- make -C iac/provider-gcp ${recovery_target}" \
    "${runbook}" >/dev/null || {
    printf 'Runbook target %s must execute from the provider Makefile.\n' \
      "${recovery_target}" >&2
    exit 1
  }
done
if grep -Eq '^[[:space:]]+make workload-cluster-(recover-lease|plan|apply)' \
  "${runbook}"; then
  printf 'Runbook cannot invoke provider-only recovery targets from the repository root.\n' >&2
  exit 1
fi

apply_block="${test_dir}/workload-cluster-apply.txt"
awk '
  /^workload-cluster-apply:/ { capture=1 }
  /^workload-cluster-recover-lease:/ { capture=0 }
  capture
' "${provider_root}/Makefile" >"${apply_block}"
apply_line="$(grep -nF '$(TF) apply -input=false' "${apply_block}" | cut -d: -f1)"
lease_assert_line="$(grep -nF '"$(WORKLOAD_ROLLOUT_LEASE)" assert-held' "${apply_block}" | cut -d: -f1)"
wait_line="$(grep -nF './scripts/wait-network-hardening-stage.sh' "${apply_block}" | cut -d: -f1)"
release_line="$(grep -nF '"$(WORKLOAD_ROLLOUT_LEASE)" release' "${apply_block}" | tail -1 | cut -d: -f1)"
[[ -n "${lease_assert_line}" && -n "${apply_line}" && -n "${wait_line}" && -n "${release_line}" ]]
((lease_assert_line < apply_line && apply_line < wait_line && wait_line < release_line))
grep -F 'mutation_started=true' "${apply_block}" >/dev/null
grep -F 'convergence_proven=true' "${apply_block}" >/dev/null
grep -F 'DOMAIN_NAME="$(DOMAIN_NAME)"' "${apply_block}" >/dev/null
grep -F 'preserving the shared lease and private recovery directory' \
  "${apply_block}" >/dev/null

make_plan() {
  local stage="$1"
  local output="$2"
  jq -n --arg stage "${stage}" '
    {network:1,server:2,api:3,worker:4,build:5} as $rank
    | {network:"disabled",server:"network",api:"server",worker:"api",build:"worker"} as $previous
    | {
        network: [
          "module.cluster.module.network.google_compute_firewall.iap_remote_connection_firewall_ingress[0]",
          "module.cluster.module.network.google_compute_firewall.internal_remote_connection_firewall_ingress",
          "module.cluster.module.network.google_compute_firewall.remote_connection_firewall_ingress"
        ],
        server: [
          "module.cluster.google_compute_instance_template.server",
          "module.cluster.google_compute_region_instance_group_manager.server_pool"
        ],
        api: [
          "module.cluster.google_compute_instance_group_manager.api_pool",
          "module.cluster.google_compute_instance_template.api"
        ],
        worker: [
          "module.cluster.module.client_cluster[\"default\"].google_compute_instance_template.template",
          "module.cluster.module.client_cluster[\"default\"].google_compute_region_instance_group_manager.pool"
        ],
        build: [
          "module.cluster.google_compute_instance_group_manager.clickhouse_pool",
          "module.cluster.google_compute_instance_group_manager.loki_pool",
          "module.cluster.google_compute_instance_template.clickhouse",
          "module.cluster.google_compute_instance_template.loki",
          "module.cluster.module.build_cluster[\"default\"].google_compute_instance_template.template",
          "module.cluster.module.build_cluster[\"default\"].google_compute_region_instance_group_manager.pool"
        ]
      } as $mutations
    | [
        {address:"module.cluster.google_compute_instance_template.server", role_rank:2},
        {address:"module.cluster.google_compute_instance_template.api", role_rank:3},
        {address:"module.cluster.module.client_cluster[\"default\"].google_compute_instance_template.template", role_rank:4},
        {address:"module.cluster.module.build_cluster[\"default\"].google_compute_instance_template.template", role_rank:5},
        {address:"module.cluster.google_compute_instance_template.loki", role_rank:5},
        {address:"module.cluster.google_compute_instance_template.clickhouse", role_rank:5}
      ] as $templates
    | {
        format_version:"1.2",
        errored:false,
        resource_changes: (
          [
            {
              address:"module.cluster.terraform_data.os_login_operator_access_guard",
              mode:"managed",
              type:"terraform_data",
              change:{actions:["no-op"],before:{input:true},after:{input:true}}
            },
            {
              address:"module.cluster.terraform_data.network_hardening_rollout_completion",
              mode:"managed",
              type:"terraform_data",
              change:{
                actions:(if $stage == "network" then ["create"] else ["delete","create"] end),
                before:(if $stage == "network" then null else {input:$previous[$stage]} end),
                after:{input:$stage}
              }
            },
            {
              address:"module.cluster.terraform_data.network_hardening_rollout_stage",
              mode:"managed",
              type:"terraform_data",
              change:{actions:["update"],before:{input:$previous[$stage]},after:{input:$stage}}
            }
          ]
          + [
              $templates[]
              | . as $template
              | ($rank[$stage] >= .role_rank) as $enabled
              | {
                  address:.address,
                  mode:"managed",
                  type:"google_compute_instance_template",
                  change:{
                    actions:(if ($mutations[$stage] | index($template.address)) then ["create","delete"] else ["no-op"] end),
                    before:{metadata:{}},
                    after:{metadata:(if $enabled then {"enable-oslogin":"TRUE"} else {} end)}
                  }
                }
            ]
          + [
              $mutations[$stage][] as $address
              | select([$templates[].address] | index($address) | not)
              | {
                  address:$address,
                  mode:"managed",
                  type:(if ($address | contains("firewall")) then "google_compute_firewall" else "google_compute_instance_group_manager" end),
                  change:{
                    actions:(if ($address | contains("iap_remote_connection")) then ["create"] else ["update"] end),
                    before:(if ($address | contains("iap_remote_connection")) then null else {} end),
                    after:(
                      if ($address | contains("iap_remote_connection")) then {
                        direction:"INGRESS", priority:700,
                        source_ranges:["35.235.240.0/20"], target_tags:["orch"],
                        allow:[{protocol:"tcp",ports:["22","3389"]}], deny:[],
                        log_config:[{metadata:"EXCLUDE_ALL_METADATA"}]
                      } elif ($address | contains("internal_remote_connection")) then {
                        direction:"INGRESS", priority:900,
                        source_ranges:["0.0.0.0/0","35.235.240.0/20"], target_tags:["orch"],
                        allow:[{protocol:"tcp",ports:["22","3389"]}], deny:[],
                        log_config:[{metadata:"EXCLUDE_ALL_METADATA"}]
                      } elif ($address | contains("remote_connection_firewall_ingress")) then {
                        direction:"INGRESS", priority:800,
                        source_ranges:["0.0.0.0/0"], target_tags:["orch"],
                        allow:[], deny:[{protocol:"tcp",ports:["22","3389"]}],
                        log_config:[{metadata:"EXCLUDE_ALL_METADATA"}]
                      } else {} end
                    )
                  }
                }
            ]
        )
      }
  ' >"${output}"
  chmod 0600 "${output}"
}

for stage in network server api worker build; do
  plan="${test_dir}/${stage}.plan"
  make_plan "${stage}" "${plan}"
  "${script_dir}/assert-network-hardening-stage-plan.sh" \
    "${plan}" "${fake_terraform}" "${stage}" >/dev/null
done

jq '
  .resource_changes |= map(
    select(.address != "module.cluster.module.network.google_compute_firewall.iap_remote_connection_firewall_ingress[0]")
  )
' "${test_dir}/network.plan" >"${test_dir}/network-missing-iap-overlay.plan"
expect_fail "network stage requires the exact IAP overlay" \
  "${script_dir}/assert-network-hardening-stage-plan.sh" \
  "${test_dir}/network-missing-iap-overlay.plan" "${fake_terraform}" network

jq '
  (.resource_changes[]
    | select(.address == "module.cluster.module.network.google_compute_firewall.iap_remote_connection_firewall_ingress[0]")
    | .change.after.priority) = 900
' "${test_dir}/network.plan" >"${test_dir}/network-wrong-iap-priority.plan"
expect_fail "IAP overlay must precede the public deny" \
  "${script_dir}/assert-network-hardening-stage-plan.sh" \
  "${test_dir}/network-wrong-iap-priority.plan" "${fake_terraform}" network

jq '
  (.resource_changes[]
    | select(.address == "module.cluster.module.network.google_compute_firewall.internal_remote_connection_firewall_ingress")
    | .change.after.priority) = 750
' "${test_dir}/network.plan" >"${test_dir}/network-legacy-beats-deny.plan"
expect_fail "legacy public allow must remain shadowed by the deny" \
  "${script_dir}/assert-network-hardening-stage-plan.sh" \
  "${test_dir}/network-legacy-beats-deny.plan" "${fake_terraform}" network

jq '
  (.resource_changes[]
    | select(.address == "module.cluster.module.network.google_compute_firewall.iap_remote_connection_firewall_ingress[0]")
    | .change.after.source_ranges) = ["0.0.0.0/0", "35.235.240.0/20"]
' "${test_dir}/network.plan" >"${test_dir}/network-public-iap-overlay.plan"
expect_fail "IAP overlay cannot retain a public source" \
  "${script_dir}/assert-network-hardening-stage-plan.sh" \
  "${test_dir}/network-public-iap-overlay.plan" "${fake_terraform}" network

jq '
  (.resource_changes[]
    | select(.address == "module.cluster.module.network.google_compute_firewall.remote_connection_firewall_ingress")
    | .change.after.log_config) = []
' "${test_dir}/network.plan" >"${test_dir}/network-unlogged-deny.plan"
expect_fail "public deny must retain decision logging" \
  "${script_dir}/assert-network-hardening-stage-plan.sh" \
  "${test_dir}/network-unlogged-deny.plan" "${fake_terraform}" network

jq '
  (.resource_changes[]
    | select(.address == "module.cluster.module.network.google_compute_firewall.remote_connection_firewall_ingress")
    | .change.after.deny[0].ports) = ["22"]
' "${test_dir}/network.plan" >"${test_dir}/network-incomplete-deny.plan"
expect_fail "public deny must cover every administrative port" \
  "${script_dir}/assert-network-hardening-stage-plan.sh" \
  "${test_dir}/network-incomplete-deny.plan" "${fake_terraform}" network

# A whole-module dependency on the changing authorization guard defers the
# worker/build image-family reads and turns their otherwise-stable templates
# into replacements. The network stage must reject that exact regression even
# though its exact firewall transition remains valid.
jq '
  (.resource_changes[]
    | select(
        .address
        == "module.cluster.module.client_cluster[\"default\"].google_compute_instance_template.template"
        or .address
        == "module.cluster.module.build_cluster[\"default\"].google_compute_instance_template.template"
      )
    | .change.actions) = ["create", "delete"]
' "${test_dir}/network.plan" >"${test_dir}/network-deferred-source-image.plan"
expect_fail "network stage cannot replace worker/build templates after deferred source-image reads" \
  "${script_dir}/assert-network-hardening-stage-plan.sh" \
  "${test_dir}/network-deferred-source-image.plan" "${fake_terraform}" network

# A normal deployment can initialize the no-op sentinel while rollout is
# disabled. Its first reviewed network stage must replace that persisted value.
jq '
  (.resource_changes[]
    | select(.address == "module.cluster.terraform_data.network_hardening_rollout_completion")
    | .change.actions) = ["delete", "create"]
  | (.resource_changes[]
    | select(.address == "module.cluster.terraform_data.network_hardening_rollout_completion")
    | .change.before) = {input: "disabled"}
' "${test_dir}/network.plan" >"${test_dir}/initialized-network.plan"
"${script_dir}/assert-network-hardening-stage-plan.sh" \
  "${test_dir}/initialized-network.plan" "${fake_terraform}" network >/dev/null

# A failed stage can have committed the template while leaving the MIG pending.
# The convergence sentinel and stage marker keep the persisted stage at the
# previous value, so the remaining in-boundary mutation must be retryable.
jq '
  (.resource_changes[]
    | select(.address == "module.cluster.google_compute_instance_template.server")
    | .change.actions) = ["no-op"]
  | (.resource_changes[]
    | select(.address == "module.cluster.terraform_data.network_hardening_rollout_completion")
    | .change.before.input) = "server"
' "${test_dir}/server.plan" >"${test_dir}/partial-retry.plan"
"${script_dir}/assert-network-hardening-stage-plan.sh" \
  "${test_dir}/partial-retry.plan" "${fake_terraform}" server >/dev/null

# If convergence completed but persisting the marker failed, every pool
# resource can be a no-op on the retry, but the sentinel must still be replaced
# so live convergence is re-proven inside the apply graph.
jq '
  (.resource_changes[]
    | select(.address == "module.cluster.terraform_data.network_hardening_rollout_completion")
    | .change.before.input) = "server"
  | (.resource_changes[]
    | select(.address == "module.cluster.google_compute_instance_template.server")
    | .change.actions) = ["no-op"]
  | (.resource_changes[]
    | select(.address == "module.cluster.google_compute_region_instance_group_manager.server_pool")
    | .change.actions) = ["no-op"]
' "${test_dir}/server.plan" >"${test_dir}/marker-retry.plan"
"${script_dir}/assert-network-hardening-stage-plan.sh" \
  "${test_dir}/marker-retry.plan" "${fake_terraform}" server >/dev/null

# A forced same-stage retry can destroy the convergence sentinel before its
# replacement is persisted. The current marker must be allowed to repair that
# missing sentinel, while the following stage must remain blocked.
jq '
  (.resource_changes[]
    | select(.address == "module.cluster.terraform_data.network_hardening_rollout_completion")
    | .change.actions) = ["create"]
  | (.resource_changes[]
    | select(.address == "module.cluster.terraform_data.network_hardening_rollout_completion")
    | .change.before) = null
  | (.resource_changes[]
    | select(.address == "module.cluster.terraform_data.network_hardening_rollout_stage")
    | .change.before.input) = "server"
  | (.resource_changes[]
    | select(.address == "module.cluster.terraform_data.network_hardening_rollout_stage")
    | .change.actions) = ["no-op"]
' "${test_dir}/server.plan" >"${test_dir}/missing-sentinel-retry.plan"
"${script_dir}/assert-network-hardening-stage-plan.sh" \
  "${test_dir}/missing-sentinel-retry.plan" "${fake_terraform}" server server >/dev/null
expect_fail "absent current-stage sentinel requires validated recovery context" \
  "${script_dir}/assert-network-hardening-stage-plan.sh" \
  "${test_dir}/missing-sentinel-retry.plan" "${fake_terraform}" server

# An initial stage transition can likewise be interrupted after Terraform
# destroys the previous sentinel but before either its replacement or the
# downstream marker is persisted. Only the exact previous -> requested marker
# transition may recreate that absent sentinel.
jq '
  (.resource_changes[]
    | select(.address == "module.cluster.terraform_data.network_hardening_rollout_completion")
    | .change.actions) = ["create"]
  | (.resource_changes[]
    | select(.address == "module.cluster.terraform_data.network_hardening_rollout_completion")
    | .change.before) = null
' "${test_dir}/server.plan" >"${test_dir}/missing-initial-sentinel-retry.plan"
"${script_dir}/assert-network-hardening-stage-plan.sh" \
  "${test_dir}/missing-initial-sentinel-retry.plan" "${fake_terraform}" server server >/dev/null
expect_fail "absent initial-stage sentinel requires validated recovery context" \
  "${script_dir}/assert-network-hardening-stage-plan.sh" \
  "${test_dir}/missing-initial-sentinel-retry.plan" "${fake_terraform}" server

jq '
  (.resource_changes[]
    | select(.address == "module.cluster.terraform_data.network_hardening_rollout_stage")
    | .change.before.input) = "disabled"
' "${test_dir}/missing-initial-sentinel-retry.plan" \
  >"${test_dir}/missing-initial-sentinel-skipped-marker.plan"
expect_fail "absent sentinel cannot weaken the exact previous-stage marker" \
  "${script_dir}/assert-network-hardening-stage-plan.sh" \
  "${test_dir}/missing-initial-sentinel-skipped-marker.plan" "${fake_terraform}" server server

jq '
  (.resource_changes[]
    | select(.address == "module.cluster.terraform_data.network_hardening_rollout_completion")
    | .change.actions) = ["create"]
  | (.resource_changes[]
    | select(.address == "module.cluster.terraform_data.network_hardening_rollout_completion")
    | .change.before) = null
' "${test_dir}/api.plan" >"${test_dir}/missing-previous-sentinel.plan"
expect_fail "missing previous-stage sentinel cannot admit the following stage" \
  "${script_dir}/assert-network-hardening-stage-plan.sh" \
  "${test_dir}/missing-previous-sentinel.plan" "${fake_terraform}" api

# A successful apply can advance the marker and still leave same-stage drift
# for the post-apply plan to detect. The borrowed-token retry must accept the
# current marker as a no-op while the forced sentinel replacement re-proves
# live convergence before the remaining bounded mutation is applied.
jq '
  (.resource_changes[]
    | select(.address == "module.cluster.terraform_data.network_hardening_rollout_completion")
    | .change.before.input) = "server"
  | (.resource_changes[]
    | select(.address == "module.cluster.terraform_data.network_hardening_rollout_stage")
    | .change.before.input) = "server"
  | (.resource_changes[]
    | select(.address == "module.cluster.terraform_data.network_hardening_rollout_stage")
    | .change.actions) = ["no-op"]
  | (.resource_changes[]
    | select(.address == "module.cluster.google_compute_instance_template.server")
    | .change.actions) = ["no-op"]
' "${test_dir}/server.plan" >"${test_dir}/post-apply-drift-retry.plan"
"${script_dir}/assert-network-hardening-stage-plan.sh" \
  "${test_dir}/post-apply-drift-retry.plan" "${fake_terraform}" server >/dev/null

jq '
  (.resource_changes[]
    | select(.address == "module.cluster.terraform_data.network_hardening_rollout_stage")
    | .change.actions) = ["update"]
' "${test_dir}/post-apply-drift-retry.plan" >"${test_dir}/post-apply-marker-update.plan"
expect_fail "same-stage retry cannot mutate the persisted marker" \
  "${script_dir}/assert-network-hardening-stage-plan.sh" \
  "${test_dir}/post-apply-marker-update.plan" "${fake_terraform}" server

jq '
  (.resource_changes[]
    | select(.address == "module.cluster.terraform_data.network_hardening_rollout_completion")
    | .change.before.input) = "network"
' "${test_dir}/post-apply-drift-retry.plan" >"${test_dir}/mismatched-current-marker.plan"
expect_fail "current-stage marker requires current-stage convergence replacement" \
  "${script_dir}/assert-network-hardening-stage-plan.sh" \
  "${test_dir}/mismatched-current-marker.plan" "${fake_terraform}" server

jq '
  (.resource_changes[]
    | select(.address == "module.cluster.terraform_data.network_hardening_rollout_completion")
    | .change.actions) = ["no-op"]
' "${test_dir}/post-apply-drift-retry.plan" >"${test_dir}/marker-retry-without-convergence.plan"
expect_fail "marker retry without forced convergence replacement" \
  "${script_dir}/assert-network-hardening-stage-plan.sh" \
  "${test_dir}/marker-retry-without-convergence.plan" "${fake_terraform}" server

jq '(.resource_changes[] | select(.address == "module.cluster.terraform_data.os_login_operator_access_guard") | .change.after.input) = false' \
  "${test_dir}/server.plan" >"${test_dir}/closed.plan"
expect_fail "closed in-graph authorization guard" \
  "${script_dir}/assert-network-hardening-stage-plan.sh" \
  "${test_dir}/closed.plan" "${fake_terraform}" server

jq '(.resource_changes[] | select(.address == "module.cluster.terraform_data.network_hardening_rollout_stage") | .change.before.input) = "disabled"' \
  "${test_dir}/worker.plan" >"${test_dir}/skipped.plan"
expect_fail "skipped serial stage" \
  "${script_dir}/assert-network-hardening-stage-plan.sh" \
  "${test_dir}/skipped.plan" "${fake_terraform}" worker

jq '(.resource_changes[] | select(.address == "module.cluster.terraform_data.network_hardening_rollout_stage") | .change.before.input) = "server"' \
  "${test_dir}/network.plan" >"${test_dir}/rollback.plan"
expect_fail "reverse stage rollback" \
  "${script_dir}/assert-network-hardening-stage-plan.sh" \
  "${test_dir}/rollback.plan" "${fake_terraform}" network

jq '.resource_changes += [{address:"module.cluster.module.client_cluster[\"default\"].google_compute_region_autoscaler.autoscaler[0]",mode:"managed",type:"google_compute_region_autoscaler",change:{actions:["delete"],before:{},after:null}}]' \
  "${test_dir}/worker.plan" >"${test_dir}/ownership.plan"
expect_fail "generic autoscaler ownership mutation" \
  "${script_dir}/assert-network-hardening-stage-plan.sh" \
  "${test_dir}/ownership.plan" "${fake_terraform}" worker

now="$(date -u +%s)"
git_head="$(git -C "${repo_root}" rev-parse --verify HEAD)"
checkpoint="${test_dir}/checkpoint.json"
jq -n \
  --arg head "${git_head}" \
  --argjson now "${now}" '
    {
      schema_version:1,
      stage:"worker",
      gcp_project_id:"monad-code",
      gcp_region:"us-east4",
      gcp_zone:"us-east4-c",
      prefix:"e2b-",
      source_git_head:$head,
      operator_principal:"operator@example.invalid",
      observed_unix:$now,
      expires_unix:($now + 900),
      checks:{
        durable_sessions_preserved:true,
        iap_tunnel_access:true,
        os_login_admin_access:true,
        target_pool_drained:true,
        zero_target_allocations:true,
        zero_target_workcells:true
      },
      evidence:{
        durable_sessions_preserved:"inventory://durable",
        iap_tunnel_access:"gcloud://iap",
        os_login_admin_access:"gcloud://os-login",
        target_pool_drained:"nomad://drain",
        zero_target_allocations:"nomad://allocations",
        zero_target_workcells:"e2b://inventory"
      }
    }
  ' >"${checkpoint}"
chmod 0600 "${checkpoint}"
"${script_dir}/assert-network-hardening-checkpoint.sh" \
  worker "${checkpoint}" monad-code us-east4 us-east4-c e2b- "${repo_root}" >/dev/null

jq '.expires_unix = 1' "${checkpoint}" >"${test_dir}/stale.json"
chmod 0600 "${test_dir}/stale.json"
expect_fail "stale operator checkpoint" \
  "${script_dir}/assert-network-hardening-checkpoint.sh" \
  worker "${test_dir}/stale.json" monad-code us-east4 us-east4-c e2b- "${repo_root}"

printf 'Network-hardening rollout guards passed.\n'
