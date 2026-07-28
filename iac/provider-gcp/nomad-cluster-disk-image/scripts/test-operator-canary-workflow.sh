#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
config_root="$(cd "${script_dir}/.." && pwd)"
provider_root="$(cd "${config_root}/.." && pwd)"
repo_root="$(cd "${config_root}/../../.." && pwd)"
temp_dir="$(mktemp -d "${TMPDIR:-/tmp}/operator-canary-test.XXXXXX")"
trap 'rm -rf -- "${temp_dir}"' EXIT HUP INT TERM

fake_terraform="${temp_dir}/terraform"
cat >"${fake_terraform}" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
[[ "$1 $2" == "show -json" ]]
cat "$3"
EOF
chmod 0755 "${fake_terraform}"

base_plan="${temp_dir}/plan.json"
jq -n '{
  format_version: "1.2",
  terraform_version: "1.7.5",
  errored: false,
  checks: [{status: "pass"}],
  resource_drift: [],
  output_changes: {},
  variables: {
    gcp_project_id: {value: "test-project"},
    gcp_region: {value: "us-east4"},
    network_name: {value: "e2b-build-cluster-disk-image"},
    subnet_name: {value: "e2b-build-cluster-disk-image-subnetwork"}
  },
  resource_changes: [
    {
      address: "google_compute_network.packer_network",
      mode: "managed",
      change: {
        actions: ["create"],
        after_sensitive: {params: []},
        after_unknown: {id: true, self_link: true},
        after: {
          project: "test-project",
          name: "e2b-build-cluster-disk-image",
          auto_create_subnetworks: false,
          delete_default_routes_on_create: false,
          enable_ula_internal_ipv6: false,
          mtu: 1460,
          network_firewall_policy_enforcement_order: "AFTER_CLASSIC_FIREWALL",
          routing_mode: "REGIONAL"
        }
      }
    },
    {
      address: "google_compute_subnetwork.packer_subnetwork",
      mode: "managed",
      change: {
        actions: ["create"],
        after_sensitive: {
          log_config: [{}],
          params: [],
          secondary_ip_range: []
        },
        after_unknown: {id: true, network: true, self_link: true},
        after: {
          project: "test-project",
          region: "us-east4",
          name: "e2b-build-cluster-disk-image-subnetwork",
          ip_cidr_range: "10.0.0.0/8",
          network: "https://example/global/networks/e2b-build-cluster-disk-image",
          private_ip_google_access: false,
          send_secondary_ip_range_if_empty: false,
          stack_type: "IPV4_ONLY",
          log_config: [{
            aggregation_interval: "INTERVAL_15_MIN",
            flow_sampling: 0,
            metadata: "EXCLUDE_ALL_METADATA"
          }]
        }
      }
    },
    {
      address: "google_compute_firewall.internal_remote_connection_firewall_ingress",
      mode: "managed",
      change: {
        actions: ["create"],
        after_sensitive: {
          allow: [{ports: [false]}],
          deny: [],
          destination_ranges: [],
          source_ranges: [false]
        },
        after_unknown: {
          destination_ranges: true,
          id: true,
          self_link: true
        },
        after: {
          project: "test-project",
          name: "e2b-build-cluster-disk-image-firewall-ingress",
          network: "e2b-build-cluster-disk-image",
          direction: "INGRESS",
          priority: 900,
          disabled: false,
          destination_ranges: null,
          source_ranges: ["35.235.240.0/20"],
          source_service_accounts: null,
          source_tags: null,
          target_service_accounts: null,
          target_tags: null,
          allow: [{ports: ["22"], protocol: "tcp"}],
          deny: []
        }
      }
    }
  ]
}' >"${base_plan}"

assert_plan() {
  "${script_dir}/assert-network-plan.sh" \
    "$1" "${fake_terraform}" test-project us-east4 \
    e2b-build-cluster-disk-image \
    e2b-build-cluster-disk-image-subnetwork >/dev/null
}

assert_rejected() {
  local filter="$1"
  local label="$2"
  local candidate="${temp_dir}/${label}.json"
  jq "${filter}" "${base_plan}" >"${candidate}"
  if assert_plan "${candidate}" >/dev/null 2>&1; then
    printf 'Unsafe plan mutation unexpectedly passed: %s\n' "${label}" >&2
    exit 1
  fi
}

assert_plan "${base_plan}"
assert_rejected \
  '(.resource_changes[0].change.actions) = ["delete"]' \
  destructive
assert_rejected \
  '(.resource_changes[0].change.actions) = ["update"]' \
  update
assert_rejected \
  '.resource_changes += [{
    address: "google_compute_instance.unreviewed",
    mode: "managed",
    change: {actions: ["create"], after_sensitive: {}, after: {}}
  }]' \
  extra-resource
assert_rejected \
  '(.resource_changes[2].change.after.source_ranges) = ["0.0.0.0/0"]' \
  firewall-source
assert_rejected \
  '(.resource_changes[1].change.after.ip_cidr_range) = "10.0.0.0/7"' \
  subnet-cidr
assert_rejected \
  '(.resource_changes[0].change.after.project) = "other-project"' \
  wrong-project
assert_rejected \
  '(.resource_changes[0].change.after_sensitive) = {name: true}' \
  sensitive
assert_rejected \
  '.resource_drift = [{address: "google_compute_network.packer_network"}]' \
  drift
assert_rejected \
  '.checks = [{status: "unknown"}]' \
  unknown-check
assert_rejected \
  '.terraform_version = "1.8.0"' \
  terraform-version

if make -C "${config_root}" init ENV=dev >/dev/null 2>&1; then
  printf 'Legacy init unexpectedly succeeded.\n' >&2
  exit 1
fi
if make -C "${config_root}" build ENV=dev >/dev/null 2>&1; then
  printf 'Legacy build unexpectedly succeeded.\n' >&2
  exit 1
fi
if grep -Eq -- '(-auto-approve|-upgrade|(^|[[:space:]])-force)' \
  "${config_root}/Makefile"; then
  printf 'Operator Makefile contains a forbidden mutation bypass.\n' >&2
  exit 1
fi

apply_recipe="$(
  awk '
    /^operator-canary-apply-build:/ { capture = 1 }
    capture && /^\.PHONY: operator-canary-lease-inspect/ { exit }
    capture { print }
  ' "${config_root}/Makefile"
)"
assert_lease_first() {
  local recipe="$1"
  local acquire_line
  local check_line
  local check

  acquire_line="$(
    grep -nF '"$(ROLLOUT_LEASE)" acquire' <<<"${recipe}" |
      cut -d: -f1
  )"
  [[ "${acquire_line}" =~ ^[0-9]+$ ]] || return 1

  for check in \
    './scripts/assert-packer-plugin.sh' \
    'assert-foundation-backend.sh' \
    'assert-foundation-workspace.sh' \
    'assert-foundation-state-bucket.sh' \
    '"$(PACKER_RESERVE_GUARD)"' \
    './scripts/assert-live-capacity.sh' \
    './scripts/network-plan-metadata.sh verify ' \
    './scripts/assert-network-plan.sh'; do
    check_line="$(grep -nF "${check}" <<<"${recipe}" | cut -d: -f1)"
    [[ "${check_line}" =~ ^[0-9]+$ && "${check_line}" -gt "${acquire_line}" ]] ||
      return 1
  done
}
assert_lease_first "${apply_recipe}" || {
  printf 'Final apply/build checks are not all guarded by the shared lease.\n' >&2
  exit 1
}
adversarial_recipe="$(
  sed '/"$(ROLLOUT_LEASE)" acquire/i\
\t./scripts/assert-live-capacity.sh adversarial;' <<<"${apply_recipe}"
)"
if assert_lease_first "${adversarial_recipe}"; then
  printf 'A live capacity check injected before lease acquisition passed ordering.\n' >&2
  exit 1
fi
grep -F \
  'test "$(TERRAFORM_STATE_BUCKET)" = "$(GCP_PROJECT_ID)-terraform-state"' \
  "${config_root}/Makefile" >/dev/null || {
  printf 'Operator canary does not enforce the canonical shared lock bucket.\n' >&2
  exit 1
}

tampered_artifact_lock="${temp_dir}/root-artifacts.tampered.json"
jq '.docker_ce.sha256 = "not-a-sha256"' \
  "${config_root}/setup/root-artifacts.lock.json" \
  >"${tampered_artifact_lock}"
if "${script_dir}/assert-packer-config.sh" \
  "${config_root}/main.pkr.hcl" \
  "${config_root}/variables.pkr.hcl" \
  "${tampered_artifact_lock}" >/dev/null 2>&1; then
  printf 'Tampered root artifact identity unexpectedly passed.\n' >&2
  exit 1
fi
if rg -n \
  'get\.docker\.com|add-google-cloud-ops-agent-repo|git clone|CHECKSUM_URL' \
  "${config_root}/main.pkr.hcl" \
  "${repo_root}/iac/nomad-cluster-disk-image/setup" >/dev/null; then
  printf 'Mutable or same-origin-checksum root input remains in the canary.\n' >&2
  exit 1
fi

artifact_lock_sha="$(
  shasum -a 256 "${config_root}/setup/root-artifacts.lock.json" |
    awk '{print $1}'
)"
packer_manifest="${temp_dir}/packer-manifest.json"
jq -n \
  --arg lock_sha "${artifact_lock_sha}" '{
  last_run_uuid: "build-uuid",
  builds: [{
    name: "orch",
    builder_type: "googlecompute",
    artifact_id: "test-project/candidate-image",
    custom_data: {
      environment: "dev",
      image_family: "e2b-orch-dev-candidate",
      image_name: "candidate-image",
      source_image: "ubuntu-2404-noble-amd64-v20260723",
      source_project: "ubuntu-os-cloud",
      source_revision: "0123456789abcdef0123456789abcdef01234567",
      root_input_lock_sha256: $lock_sha
    }
  }]
}' >"${packer_manifest}"
"${script_dir}/assert-packer-artifact.sh" \
  "${packer_manifest}" test-project candidate-image \
  e2b-orch-dev-candidate dev \
  0123456789abcdef0123456789abcdef01234567 \
  ubuntu-2404-noble-amd64-v20260723 "${artifact_lock_sha}" >/dev/null
if "${script_dir}/assert-packer-artifact.sh" \
  "${packer_manifest}" test-project candidate-image \
  e2b-orch-dev-candidate dev \
  0123456789abcdef0123456789abcdef01234567 \
  ubuntu-2404-noble-amd64-v20260723 \
  aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa \
  >/dev/null 2>&1; then
  printf 'Packer manifest with the wrong root-input identity unexpectedly passed.\n' >&2
  exit 1
fi

toolchain_terraform="${temp_dir}/toolchain-terraform"
cat >"${toolchain_terraform}" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
[[ "$1" == "version" ]]
printf '{"terraform_version":"1.7.5"}\n'
EOF
toolchain_packer="${temp_dir}/toolchain-packer"
cat >"${toolchain_packer}" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
[[ "$1" == "version" ]]
printf '0,,version,1.13.1\n'
EOF
toolchain_gcloud="${temp_dir}/toolchain-gcloud"
cat >"${toolchain_gcloud}" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
[[ "$1" == "version" ]]
printf '{"Google Cloud SDK":"534.0.0"}\n'
EOF
chmod 0755 \
  "${toolchain_terraform}" "${toolchain_packer}" "${toolchain_gcloud}"
make -C "${config_root}" operator-canary-toolchain-guard \
  TF="${toolchain_terraform}" \
  PACKER="${toolchain_packer}" \
  GCLOUD="${toolchain_gcloud}" >/dev/null

pin_probe="${temp_dir}/pin-probe.mk"
cat >"${pin_probe}" <<EOF
include ${config_root}/Makefile
.PHONY: print-operator-pins
print-operator-pins:
	@printf '%s\\n' "\$(CONFIG_ROOT)|\$(EXPECTED_GCLOUD_VERSION)|\$(PACKER_SOURCE_IMAGE)|\$(PACKER_GATE_MAX_PLAN_AGE_SECONDS)|\$(ROOT_ARTIFACT_LOCK)|\$(POLICY_PATH)|\$(PACKER_SMOKE_MACHINE_TYPE)|\$(ROLLOUT_LEASE)|\$(origin tf_vars)|\$(origin packer_vars)|\$(origin metadata_context)"
EOF
resolved_pins="$(
  make -s --no-print-directory -C "${config_root}" -f "${pin_probe}" \
    print-operator-pins \
    CURDIR=/tmp/attacker-root \
    EXPECTED_GCLOUD_VERSION=577.0.0 \
    PACKER_SOURCE_IMAGE=ubuntu-attacker-image \
    PACKER_GATE_MAX_PLAN_AGE_SECONDS=999999 \
    ROOT_ARTIFACT_LOCK=/tmp/attacker-lock.json \
    POLICY_PATH=/tmp/attacker-policy.json \
    PACKER_SMOKE_MACHINE_TYPE=n1-highmem-96 \
    ROLLOUT_LEASE=/bin/true \
    tf_vars=attacker \
    packer_vars=attacker \
    metadata_context=attacker
)"
expected_pins="${config_root}|534.0.0|ubuntu-2404-noble-amd64-v20260723|3600|${config_root}/setup/root-artifacts.lock.json|${provider_root}/topology/minimal-workload-policy.json|n1-standard-4|${provider_root}/scripts/rollout-mutation-lease.sh|override|override|override"
if [[ "${resolved_pins}" != "${expected_pins}" ]]; then
  printf 'Command-line values overrode repository-pinned canary inputs: %s\n' \
    "${resolved_pins}" >&2
  exit 1
fi

plugin_root="${temp_dir}/plugins"
plugin_path="${plugin_root}/github.com/hashicorp/googlecompute/packer-plugin-googlecompute_v1.0.16_x5.0_linux_amd64"
mkdir -p "$(dirname "${plugin_path}")"
printf 'reviewed plugin bytes' >"${plugin_path}"
chmod 0755 "${plugin_path}"
shasum -a 256 "${plugin_path}" | awk '{print $1}' >"${plugin_path}_SHA256SUM"
fake_packer="${temp_dir}/packer"
cat >"${fake_packer}" <<EOF
#!/usr/bin/env bash
set -euo pipefail
printf '0,,ui,message,%s\\n' '${plugin_path}'
EOF
chmod 0755 "${fake_packer}"
"${script_dir}/assert-packer-plugin.sh" \
  "${fake_packer}" "${plugin_root}" >/dev/null
printf 'tamper' >>"${plugin_path}"
if "${script_dir}/assert-packer-plugin.sh" \
  "${fake_packer}" "${plugin_root}" >/dev/null 2>&1; then
  printf 'Tampered Packer plugin unexpectedly passed.\n' >&2
  exit 1
fi

manifest_a="${temp_dir}/manifest-a.json"
manifest_b="${temp_dir}/manifest-b.json"
jq -n '{
  schema_version: 2,
  environment: "dev",
  gcp_project_id: "test-project",
  image_name: "candidate-a"
}' >"${manifest_a}"
jq '.image_name = "candidate-b"' "${manifest_a}" >"${manifest_b}"
chmod 0600 "${manifest_a}" "${manifest_b}"
confirmation_a="$(
  "${script_dir}/network-plan-metadata.sh" confirmation "${manifest_a}"
)"
confirmation_b="$(
  "${script_dir}/network-plan-metadata.sh" confirmation "${manifest_b}"
)"
[[ "${confirmation_a}" != "${confirmation_b}" ]] || {
  printf 'Confirmation was reusable across different manifests.\n' >&2
  exit 1
}

identity_plugin_root="${temp_dir}/identity-plugins"
identity_plugin_path="${identity_plugin_root}/github.com/hashicorp/googlecompute/packer-plugin-googlecompute_v1.0.16_x5.0_linux_amd64"
mkdir -p "$(dirname "${identity_plugin_path}")"
printf 'identity plugin bytes' >"${identity_plugin_path}"
chmod 0755 "${identity_plugin_path}"
shasum -a 256 "${identity_plugin_path}" |
  awk '{print $1}' >"${identity_plugin_path}_SHA256SUM"
identity_packer="${temp_dir}/identity-packer"
cat >"${identity_packer}" <<EOF
#!/usr/bin/env bash
set -euo pipefail
if [[ "\$1" == "version" ]]; then
  printf '0,,version,1.13.1\\n'
  for i in {1..5000}; do
    printf '0,,ui,message,trailing-version-output-%s\\n' "\$i"
  done
elif [[ "\$1 \$2" == "plugins installed" ]]; then
  printf '0,,ui,message,%s\\n' '${identity_plugin_path}'
else
  exit 1
fi
EOF
chmod 0755 "${identity_packer}"
identity_terraform="${temp_dir}/identity-terraform"
cat >"${identity_terraform}" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
if [[ "$1" == "version" ]]; then
  printf '{"terraform_version":"1.7.5"}\n'
elif [[ "$1 $2" == "state pull" ]]; then
  jq -n --argjson serial "${FAKE_STATE_SERIAL:-7}" \
    '{lineage: "test-lineage", serial: $serial}'
else
  exit 1
fi
EOF
chmod 0755 "${identity_terraform}"
identity_gcloud="${temp_dir}/identity-gcloud"
cat >"${identity_gcloud}" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
if [[ "$1" == "version" ]]; then
  printf '{"Google Cloud SDK":"534.0.0"}\n'
elif [[ "$1 $2 $3" == "compute images describe" ]]; then
  jq -n --arg id "${FAKE_SOURCE_ID:-123456}" '{
    archiveSizeBytes: "1000",
    deprecated: null,
    diskSizeGb: "10",
    id: $id,
    name: "ubuntu-2404-noble-amd64-v20260723",
    selfLink: (
      "https://www.googleapis.com/compute/v1/projects/ubuntu-os-cloud/global/images/"
      + "ubuntu-2404-noble-amd64-v20260723"
    ),
    status: "READY"
  }'
else
  exit 1
fi
EOF
chmod 0755 "${identity_gcloud}"
identity_env="${temp_dir}/.env.dev"
printf 'GCP_PROJECT_ID=test-project\n' >"${identity_env}"
chmod 0600 "${identity_env}"
identity_plan="${temp_dir}/identity-plan"
cp "${base_plan}" "${identity_plan}"
chmod 0600 "${identity_plan}"
identity_manifest="${temp_dir}/identity-manifest"
source_revision="$(git -C "${repo_root}" rev-parse --verify HEAD)"
metadata_env=(
  "PACKER_GATE_ENV=dev"
  "PACKER_GATE_ENV_FILE=${identity_env}"
  "PACKER_GATE_GCP_PROJECT_ID=test-project"
  "PACKER_GATE_GCP_REGION=us-east4"
  "PACKER_GATE_GCP_ZONE=us-east4-a"
  "PACKER_GATE_STATE_BUCKET=state-bucket"
  "PACKER_GATE_STATE_PREFIX=terraform/cluster-disk-image/state"
  "PACKER_GATE_NETWORK_NAME=e2b-build-cluster-disk-image"
  "PACKER_GATE_SUBNET_NAME=e2b-build-cluster-disk-image-subnetwork"
  "PACKER_GATE_CONSUL_VERSION=1.17.3"
  "PACKER_GATE_NOMAD_VERSION=1.8.4"
  "PACKER_GATE_SOURCE_IMAGE=ubuntu-2404-noble-amd64-v20260723"
  "PACKER_GATE_IMAGE_NAME=e2b-orch-dev-candidate-${source_revision:0:12}"
  "PACKER_GATE_IMAGE_FAMILY=e2b-orch-dev-candidate"
  "PACKER_GATE_CANONICAL_IMAGE_FAMILY=e2b-orch"
  "PACKER_GATE_SOURCE_REVISION=${source_revision}"
  "PACKER_GATE_MAX_PLAN_AGE_SECONDS=3600"
  "PACKER_PLUGIN_PATH=${identity_plugin_root}"
)
fingerprint="$(
  env "${metadata_env[@]}" \
    "${script_dir}/network-plan-metadata.sh" fingerprint \
    "${identity_terraform}" "${identity_packer}" "${identity_gcloud}" \
    "${config_root}" "${repo_root}"
)"
env "${metadata_env[@]}" \
  "${script_dir}/network-plan-metadata.sh" write \
  "${identity_plan}" "${identity_manifest}" \
  "${identity_terraform}" "${identity_packer}" "${identity_gcloud}" \
  "${config_root}" "${repo_root}" "${fingerprint}"
env "${metadata_env[@]}" \
  "${script_dir}/network-plan-metadata.sh" verify \
  "${identity_plan}" "${identity_manifest}" \
  "${identity_terraform}" "${identity_packer}" "${identity_gcloud}" \
  "${config_root}" "${repo_root}" >/dev/null
if FAKE_STATE_SERIAL=8 env "${metadata_env[@]}" \
  "${script_dir}/network-plan-metadata.sh" verify \
  "${identity_plan}" "${identity_manifest}" \
  "${identity_terraform}" "${identity_packer}" "${identity_gcloud}" \
  "${config_root}" "${repo_root}" >/dev/null 2>&1; then
  printf 'Changed Terraform state serial unexpectedly passed provenance.\n' >&2
  exit 1
fi
if FAKE_SOURCE_ID=654321 env "${metadata_env[@]}" \
  "${script_dir}/network-plan-metadata.sh" verify \
  "${identity_plan}" "${identity_manifest}" \
  "${identity_terraform}" "${identity_packer}" "${identity_gcloud}" \
  "${config_root}" "${repo_root}" >/dev/null 2>&1; then
  printf 'Changed source-image identity unexpectedly passed provenance.\n' >&2
  exit 1
fi
expired_manifest="${temp_dir}/expired-manifest"
jq '.created_at_epoch = 1' "${identity_manifest}" >"${expired_manifest}"
chmod 0600 "${expired_manifest}"
if env "${metadata_env[@]}" \
  "${script_dir}/network-plan-metadata.sh" verify \
  "${identity_plan}" "${expired_manifest}" \
  "${identity_terraform}" "${identity_packer}" "${identity_gcloud}" \
  "${config_root}" "${repo_root}" >/dev/null 2>&1; then
  printf 'Expired saved plan unexpectedly passed provenance.\n' >&2
  exit 1
fi

fixture_repo="${temp_dir}/repo"
fixture_config="${fixture_repo}/iac/provider-gcp/nomad-cluster-disk-image"
mkdir -p \
  "${fixture_config}" \
  "${fixture_repo}/iac/nomad-cluster-disk-image/setup"
touch \
  "${fixture_config}/main.tf" \
  "${fixture_config}/variables.tf" \
  "${fixture_config}/main.pkr.hcl" \
  "${fixture_config}/variables.pkr.hcl"
ln -s /tmp "${fixture_repo}/iac/nomad-cluster-disk-image/setup/unsafe"
if PACKER_PLUGIN_PATH="${plugin_root}" \
  "${script_dir}/assert-operator-inputs.sh" \
  "${fixture_config}" "${fixture_repo}" "${plugin_root}" false \
  >/dev/null 2>&1; then
  printf 'Symlinked Packer setup input unexpectedly passed.\n' >&2
  exit 1
fi
if TF_CLI_ARGS_plan=-destroy PACKER_PLUGIN_PATH="${plugin_root}" \
  "${script_dir}/assert-operator-inputs.sh" \
  "${config_root}" "${repo_root}" "${plugin_root}" false \
  >/dev/null 2>&1; then
  printf 'Ambient Terraform arguments unexpectedly passed.\n' >&2
  exit 1
fi

quota_policy="${repo_root}/iac/provider-gcp/topology/minimal-workload-policy.json"
fake_quota_gcloud="${temp_dir}/quota-gcloud"
cat >"${fake_quota_gcloud}" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
if [[ "$1 $2 $3" == "compute project-info describe" ]]; then
  jq -n '{
    name: "test-project",
    quotas: [{metric: "CPUS_ALL_REGIONS", limit: 64, usage: 0}]
  }'
elif [[ "$1 $2 $3" == "compute regions describe" ]]; then
  cpu_limit="${FAKE_CPU_LIMIT:-200}"
  jq -n --argjson cpu_limit "${cpu_limit}" '{
    name: "us-east4",
    quotas: [
      {metric: "CPUS", limit: $cpu_limit, usage: 0},
      {metric: "INSTANCES", limit: 32, usage: 0},
      {metric: "SSD_TOTAL_GB", limit: 500, usage: 0},
      {metric: "DISKS_TOTAL_GB", limit: 4096, usage: 0},
      {metric: "LOCAL_SSD_TOTAL_GB", limit: 6000, usage: 0},
      {metric: "IN_USE_ADDRESSES", limit: 8, usage: 0}
    ]
  }'
else
  exit 1
fi
EOF
chmod 0755 "${fake_quota_gcloud}"
"${script_dir}/assert-live-capacity.sh" \
  "${quota_policy}" "${fake_quota_gcloud}" test-project us-east4 >/dev/null
if FAKE_CPU_LIMIT=29 "${script_dir}/assert-live-capacity.sh" \
  "${quota_policy}" "${fake_quota_gcloud}" test-project us-east4 \
  >/dev/null 2>&1; then
  printf 'Insufficient regional CPU headroom unexpectedly passed.\n' >&2
  exit 1
fi

smoke_state="${temp_dir}/smoke-instance"
smoke_log="${temp_dir}/smoke-log"
smoke_gcloud="${temp_dir}/smoke-gcloud"
cat >"${smoke_gcloud}" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
if [[ "$1 $2 $3" == "compute images describe" ]]; then
  jq -n --arg revision "0123456789abcdef0123456789abcdef01234567" '{
    name: "candidate-image",
    status: "READY",
    deprecated: null,
    id: "123456",
    selfLink: (
      "https://www.googleapis.com/compute/v1/projects/test-project/global/images/"
      + "candidate-image"
    ),
    labels: {
      monad_environment: "dev",
      monad_revision: $revision
    }
  }'
elif [[ "$1 $2 $3" == "compute instances describe" ]]; then
  [[ -f "${FAKE_SMOKE_STATE}" ]] || exit 1
  printf 'monad-smoke\n'
elif [[ "$1 $2 $3" == "compute instances create" ]]; then
  printf '%s\n' "$*" >>"${FAKE_SMOKE_LOG}"
  for argument in "$@"; do
    case "${argument}" in
      --metadata-from-file=startup-script=*)
        startup_script="${argument#--metadata-from-file=startup-script=}"
        grep -F 'docker info' "${startup_script}" >/dev/null
        grep -F 'nomad agent -dev' "${startup_script}" >/dev/null
        grep -F 'consul agent -dev' "${startup_script}" >/dev/null
        grep -F 'test -c /dev/kvm' "${startup_script}" >/dev/null
        ;;
    esac
  done
  : >"${FAKE_SMOKE_STATE}"
elif [[ "$1 $2 $3" == "compute instances get-serial-port-output" ]]; then
  printf '%s:%s\n' \
    "${FAKE_SMOKE_RESULT:-MONAD_IMAGE_SMOKE_PASS}" \
    "0123456789abcdef0123456789abcdef01234567"
elif [[ "$1 $2 $3" == "compute instances delete" ]]; then
  rm -f -- "${FAKE_SMOKE_STATE}"
else
  exit 1
fi
EOF
chmod 0755 "${smoke_gcloud}"
export FAKE_SMOKE_STATE="${smoke_state}"
export FAKE_SMOKE_LOG="${smoke_log}"
PACKER_SMOKE_MAX_ATTEMPTS=2 PACKER_SMOKE_POLL_SECONDS=0 \
  "${script_dir}/smoke-built-image.sh" \
    "${smoke_gcloud}" test-project us-east4-a candidate-image \
    e2b-build-cluster-disk-image \
    e2b-build-cluster-disk-image-subnetwork n1-standard-4 \
    29.6.2 1.8.4 1.17.3 dev \
    0123456789abcdef0123456789abcdef01234567 >/dev/null
[[ ! -e "${smoke_state}" ]]
grep -F -- '--image=candidate-image' "${smoke_log}" >/dev/null
grep -F -- '--image-project=test-project' "${smoke_log}" >/dev/null
grep -F -- '--no-address' "${smoke_log}" >/dev/null
grep -F -- '--no-service-account' "${smoke_log}" >/dev/null
grep -F -- '--no-scopes' "${smoke_log}" >/dev/null
grep -F -- '--enable-nested-virtualization' "${smoke_log}" >/dev/null

: >"${smoke_log}"
if FAKE_SMOKE_RESULT=MONAD_IMAGE_SMOKE_FAIL \
  PACKER_SMOKE_MAX_ATTEMPTS=1 PACKER_SMOKE_POLL_SECONDS=0 \
  "${script_dir}/smoke-built-image.sh" \
    "${smoke_gcloud}" test-project us-east4-a candidate-image \
    e2b-build-cluster-disk-image \
    e2b-build-cluster-disk-image-subnetwork n1-standard-4 \
    29.6.2 1.8.4 1.17.3 dev \
    0123456789abcdef0123456789abcdef01234567 >/dev/null 2>&1; then
  printf 'Failed exact-image service smoke unexpectedly passed.\n' >&2
  exit 1
fi
[[ ! -e "${smoke_state}" ]]

promotion_state="${temp_dir}/promotion-family"
promotion_log="${temp_dir}/promotion-log"
printf 'e2b-orch-dev-candidate' >"${promotion_state}"
promotion_gcloud="${temp_dir}/promotion-gcloud"
cat >"${promotion_gcloud}" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
image_json() {
  local family="$1"
  local name="${2:-candidate-image}"
  jq -n \
    --arg family "${family}" \
    --arg name "${name}" \
    --arg revision "0123456789abcdef0123456789abcdef01234567" '{
    name: $name,
    family: $family,
    status: "READY",
    deprecated: null,
    id: "123456",
    selfLink: (
      "https://www.googleapis.com/compute/v1/projects/test-project/global/images/"
      + $name
    ),
    labels: {
      monad_environment: "dev",
      monad_revision: $revision
    }
  }'
}
if [[ "$1 $2 $3" == "compute images describe" ]]; then
  image_json "$(cat "${FAKE_PROMOTION_STATE}")"
elif [[ "$1 $2 $3" == "compute images update" ]]; then
  printf 'update\n' >>"${FAKE_PROMOTION_LOG}"
  for argument in "$@"; do
    case "${argument}" in
      --family=*)
        printf '%s' "${argument#--family=}" >"${FAKE_PROMOTION_STATE}"
        ;;
    esac
  done
  if [[ "${FAKE_UPDATE_ERROR_AFTER_APPLY:-false}" == "true" ]]; then
    exit 1
  fi
elif [[ "$1 $2 $3" == "compute images describe-from-family" ]]; then
  if [[ "${FAKE_BAD_FAMILY_HEAD:-false}" == "true" ]]; then
    image_json "e2b-orch" "unverified-image"
  else
    image_json "$(cat "${FAKE_PROMOTION_STATE}")"
  fi
else
  exit 1
fi
EOF
chmod 0755 "${promotion_gcloud}"
export FAKE_PROMOTION_STATE="${promotion_state}"
export FAKE_PROMOTION_LOG="${promotion_log}"
"${script_dir}/promote-built-image.sh" \
  "${promotion_gcloud}" test-project candidate-image \
  e2b-orch-dev-candidate e2b-orch dev \
  0123456789abcdef0123456789abcdef01234567 >/dev/null
[[ "$(cat "${promotion_state}")" == "e2b-orch" ]]
[[ "$(wc -l <"${promotion_log}" | tr -d ' ')" == "1" ]]
"${script_dir}/promote-built-image.sh" \
  "${promotion_gcloud}" test-project candidate-image \
  e2b-orch-dev-candidate e2b-orch dev \
  0123456789abcdef0123456789abcdef01234567 >/dev/null
[[ "$(wc -l <"${promotion_log}" | tr -d ' ')" == "1" ]]

printf 'e2b-orch-dev-candidate' >"${promotion_state}"
: >"${promotion_log}"
FAKE_UPDATE_ERROR_AFTER_APPLY=true \
  "${script_dir}/promote-built-image.sh" \
    "${promotion_gcloud}" test-project candidate-image \
    e2b-orch-dev-candidate e2b-orch dev \
    0123456789abcdef0123456789abcdef01234567 >/dev/null
[[ "$(cat "${promotion_state}")" == "e2b-orch" ]]
[[ "$(wc -l <"${promotion_log}" | tr -d ' ')" == "1" ]]

printf 'e2b-orch-dev-candidate' >"${promotion_state}"
: >"${promotion_log}"
if "${script_dir}/promote-built-image.sh" \
  "${promotion_gcloud}" test-project candidate-image \
  e2b-orch-dev-candidate e2b-orch dev \
  ffffffffffffffffffffffffffffffffffffffff >/dev/null 2>&1; then
  printf 'Unverified candidate unexpectedly reached canonical promotion.\n' >&2
  exit 1
fi
[[ ! -s "${promotion_log}" ]]

printf 'e2b-orch-dev-candidate' >"${promotion_state}"
: >"${promotion_log}"
if FAKE_BAD_FAMILY_HEAD=true "${script_dir}/promote-built-image.sh" \
  "${promotion_gcloud}" test-project candidate-image \
  e2b-orch-dev-candidate e2b-orch dev \
  0123456789abcdef0123456789abcdef01234567 >/dev/null 2>&1; then
  printf 'Mismatched canonical family head unexpectedly passed.\n' >&2
  exit 1
fi
[[ -s "${promotion_log}" ]]

printf 'Operator-canary plan and input guard tests passed.\n'
