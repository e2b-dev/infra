#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
provider_root="$(cd "${script_dir}/.." && pwd)"
repo_root="$(cd "${provider_root}/../.." && pwd)"
test_dir="$(mktemp -d)"
trap 'rm -rf "${test_dir}"' EXIT

expect_pass() {
  local description="$1"
  shift
  if ! "$@" >"${test_dir}/stdout" 2>"${test_dir}/stderr"; then
    printf 'expected pass: %s\n' "${description}" >&2
    sed -n '1,200p' "${test_dir}/stderr" >&2
    exit 1
  fi
}

expect_fail() {
  local description="$1"
  shift
  if "$@" >"${test_dir}/stdout" 2>"${test_dir}/stderr"; then
    printf 'expected failure: %s\n' "${description}" >&2
    exit 1
  fi
}

fake_gcloud="${test_dir}/gcloud"
fake_gcloud_mode="${test_dir}/gcloud-mode"
fake_gcloud_log="${test_dir}/gcloud-log"
printf 'pass\n' >"${fake_gcloud_mode}"
: >"${fake_gcloud_log}"
cat >"${fake_gcloud}" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

mode="$(cat "${FAKE_GCLOUD_MODE_FILE}")"
printf '%s\n' "$*" >>"${FAKE_GCLOUD_LOG_FILE}"

if [[ "${1:-}" == "compute" && "${2:-}" == "project-info" ]]; then
  [[ "${mode}" != "global-read-fails" ]] || exit 1
  limit=64
  usage=0
  [[ "${mode}" != "low-global-limit" ]] || limit=29
  [[ "${mode}" != "low-global-headroom" ]] || usage=40
  [[ "${mode}" != "post-cluster-saturated" ]] || usage=64
  [[ "${mode}" != "post-cluster-reserve" ]] || usage=60
  jq -cn \
    --argjson limit "${limit}" \
    --argjson usage "${usage}" \
    '{quotas: [{metric: "CPUS_ALL_REGIONS", limit: $limit, usage: $usage}]}'
  exit 0
fi

if [[ "${1:-}" == "compute" && "${2:-}" == "regions" ]]; then
  [[ "${mode}" != "region-read-fails" ]] || exit 1
  instances_limit=32
  instances_usage=0
  regional_cpu_limit=200
  regional_cpu_usage=0
  ssd_limit=500
  ssd_usage=0
  standard_limit=4096
  standard_usage=0
  local_limit=-1
  local_usage=0
  address_limit=8
  address_usage=0
  case "${mode}" in
    low-instance-headroom) instances_usage=26 ;;
    low-regional-cpu-limit) regional_cpu_limit=29 ;;
    low-regional-cpu-headroom) regional_cpu_usage=180 ;;
    low-ssd-limit) ssd_limit=469 ;;
    low-standard-headroom) standard_usage=3700 ;;
    low-local-headroom)
      local_limit=800
      local_usage=100
      ;;
    low-address-headroom) address_usage=2 ;;
    post-cluster-saturated)
      instances_usage=32
      regional_cpu_usage=200
      ssd_usage=500
      standard_usage=4096
      local_usage=750
      address_usage=8
      ;;
    post-cluster-reserve)
      instances_usage=31
      regional_cpu_usage=196
      ssd_usage=490
      standard_usage=3896
      address_usage=7
      ;;
  esac
  jq -cn \
    --argjson instances_limit "${instances_limit}" \
    --argjson instances_usage "${instances_usage}" \
    --argjson regional_cpu_limit "${regional_cpu_limit}" \
    --argjson regional_cpu_usage "${regional_cpu_usage}" \
    --argjson ssd_limit "${ssd_limit}" \
    --argjson ssd_usage "${ssd_usage}" \
    --argjson standard_limit "${standard_limit}" \
    --argjson standard_usage "${standard_usage}" \
    --argjson local_limit "${local_limit}" \
    --argjson local_usage "${local_usage}" \
    --argjson address_limit "${address_limit}" \
    --argjson address_usage "${address_usage}" \
    --arg mode "${mode}" '
      {
        quotas: [
          {
            metric: "INSTANCES",
            limit: $instances_limit,
            usage: $instances_usage
          },
          {
            metric: "CPUS",
            limit: $regional_cpu_limit,
            usage: $regional_cpu_usage
          },
          {
            metric: "SSD_TOTAL_GB",
            limit: $ssd_limit,
            usage: $ssd_usage
          },
          {
            metric: "DISKS_TOTAL_GB",
            limit: $standard_limit,
            usage: $standard_usage
          },
          {
            metric: "LOCAL_SSD_TOTAL_GB",
            limit: $local_limit,
            usage: $local_usage
          },
          {
            metric: "IN_USE_ADDRESSES",
            limit: $address_limit,
            usage: $address_usage
          }
        ]
      }
      | if $mode == "missing-regional-metric"
        then .quotas |= map(select(.metric != "SSD_TOTAL_GB"))
        else .
        end
    '
  exit 0
fi

if [[ "${1:-}" == "compute" && "${2:-}" == "images" ]]; then
  [[ "${mode}" != "missing-orchestrator-family" ]] || exit 1
  if [[ "${mode}" == "deprecated-orchestrator-family" ]]; then
    printf '%s\n' \
      '{"name":"e2b-orch-20260728","project":"monad-code","status":"READY","selfLink":"https://www.googleapis.com/compute/v1/projects/monad-code/global/images/e2b-orch-20260728","deprecated":{"state":"DEPRECATED"}}'
  elif [[ "${mode}" == "different-orchestrator-image" ]]; then
    printf '%s\n' \
      '{"name":"e2b-orch-20260729","project":"monad-code","status":"READY","selfLink":"https://www.googleapis.com/compute/v1/projects/monad-code/global/images/e2b-orch-20260729"}'
  else
    printf '%s\n' \
      '{"name":"e2b-orch-20260728","project":"monad-code","status":"READY","selfLink":"https://www.googleapis.com/compute/v1/projects/monad-code/global/images/e2b-orch-20260728"}'
  fi
  exit 0
fi

if [[ "${1:-}" == "artifacts" && "${2:-}" == "docker" ]]; then
  reference="${5:-}"
  if [[ "${mode}" == "missing-core-revision" && "${reference}" != *:latest ]]; then
    exit 1
  fi
  if [[ "${mode}" == "missing-core-latest" && "${reference}" == *:latest ]]; then
    exit 1
  fi
  digest="sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
  if [[ "${mode}" == "different-core-digest" ]]; then
    digest="sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
  fi
  if [[ "${mode}" == "mismatched-core-tag" && "${reference}" == *:latest ]]; then
    digest="sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
  fi
  jq -cn --arg digest "${digest}" '{image_summary: {digest: $digest}}'
  exit 0
fi

if [[ "${1:-}" == "storage" && "${2:-}" == "objects" && "${3:-}" == "describe" ]]; then
  object_uri="${4:-}"
  bucket="${object_uri#gs://}"
  bucket="${bucket%%/*}"
  object_name="${object_uri#gs://${bucket}/}"
  canonical_name="${object_name%%.${FAKE_CORE_IMAGE_REVISION}}"

  case "${canonical_name}" in
    orchestrator)
      generation=1001
      size=129278600
      md5_hash="PMSUl0V18Ei9Lyj4jlVgag=="
      crc32c_hash="R/l+hQ=="
      ;;
    template-manager)
      generation=1002
      size=129278600
      md5_hash="PMSUl0V18Ei9Lyj4jlVgag=="
      crc32c_hash="R/l+hQ=="
      ;;
    clean-nfs-cache)
      generation=1003
      size=50557937
      md5_hash="zLUBMUIvTYcm+aDVzj1ZFA=="
      crc32c_hash="f4G09A=="
      ;;
    *)
      printf 'unexpected fake GCS object: %s\n' "${object_uri}" >&2
      exit 2
      ;;
  esac
  if [[ "${object_name}" != "${canonical_name}" ]]; then
    generation=$((generation + 1000))
  fi
  jq -cn \
    --arg bucket "${bucket}" \
    --arg name "${object_name}" \
    --arg generation "${generation}" \
    --argjson size "${size}" \
    --arg md5_hash "${md5_hash}" \
    --arg crc32c_hash "${crc32c_hash}" '
      {
        bucket: $bucket,
        name: $name,
        generation: $generation,
        size: $size,
        md5_hash: $md5_hash,
        crc32c_hash: $crc32c_hash
      }
    '
  exit 0
fi

printf 'unexpected fake gcloud command: %s\n' "$*" >&2
exit 2
EOF
chmod 0755 "${fake_gcloud}"
export FAKE_GCLOUD_MODE_FILE="${fake_gcloud_mode}"
export FAKE_GCLOUD_LOG_FILE="${fake_gcloud_log}"

policy="${provider_root}/topology/minimal-workload-policy.json"
expect_pass \
  "one-workcell quota with unlimited local SSD" \
  "${script_dir}/assert-workload-quota.sh" \
  "${policy}" monad-code us-east4 "${fake_gcloud}"
grep -F '"metric_limit":"unlimited"' "${test_dir}/stdout" >/dev/null

for quota_mode in \
  low-global-limit \
  low-global-headroom \
  low-instance-headroom \
  low-regional-cpu-limit \
  low-regional-cpu-headroom \
  low-ssd-limit \
  low-standard-headroom \
  low-local-headroom \
  low-address-headroom \
  missing-regional-metric \
  global-read-fails \
  region-read-fails; do
  printf '%s\n' "${quota_mode}" >"${fake_gcloud_mode}"
  expect_fail \
    "quota fixture ${quota_mode}" \
    "${script_dir}/assert-workload-quota.sh" \
    "${policy}" monad-code us-east4 "${fake_gcloud}"
done

printf 'post-cluster-saturated\n' >"${fake_gcloud_mode}"
expect_fail \
  "bootstrap quota rejects a fully consumed fleet limit" \
  "${script_dir}/assert-workload-quota.sh" \
  "${policy}" monad-code us-east4 "${fake_gcloud}" bootstrap
expect_fail \
  "post-cluster quota preserves operational reserve at saturated limits" \
  "${script_dir}/assert-workload-quota.sh" \
  "${policy}" monad-code us-east4 "${fake_gcloud}" post-cluster

printf 'post-cluster-reserve\n' >"${fake_gcloud_mode}"
expect_pass \
  "post-cluster quota admits exactly the reviewed peak-minus-base reserve" \
  "${script_dir}/assert-workload-quota.sh" \
  "${policy}" monad-code us-east4 "${fake_gcloud}" post-cluster

for quota_mode in low-global-limit missing-regional-metric; do
  printf '%s\n' "${quota_mode}" >"${fake_gcloud_mode}"
  expect_fail \
    "post-cluster quota remains fail-closed for ${quota_mode}" \
    "${script_dir}/assert-workload-quota.sh" \
    "${policy}" monad-code us-east4 "${fake_gcloud}" post-cluster
done

printf 'pass\n' >"${fake_gcloud_mode}"
expect_fail \
  "unknown quota mode" \
  "${script_dir}/assert-workload-quota.sh" \
  "${policy}" monad-code us-east4 "${fake_gcloud}" unknown

revision="0123456789ab"
job_binary_bucket="monad-code-fc-env-pipeline"
export FAKE_CORE_IMAGE_REVISION="${revision}"
printf 'pass\n' >"${fake_gcloud_mode}"
: >"${fake_gcloud_log}"
expect_pass \
  "orchestrator image and five revision-matched core images" \
  "${script_dir}/assert-workload-artifacts.sh" \
  monad-code us-east4 e2b- "${revision}" "${job_binary_bucket}" \
  "${fake_gcloud}"
test "$(wc -l <"${fake_gcloud_log}" | tr -d ' ')" -eq 17
for image in \
  api \
  db-migrator \
  client-proxy \
  docker-reverse-proxy \
  clickhouse-migrator; do
  grep -F \
    "us-east4-docker.pkg.dev/monad-code/e2b-core/${image}:${revision}" \
    "${fake_gcloud_log}" >/dev/null
  grep -F \
    "us-east4-docker.pkg.dev/monad-code/e2b-core/${image}:latest" \
    "${fake_gcloud_log}" >/dev/null
done

for artifact_mode in \
  missing-orchestrator-family \
  deprecated-orchestrator-family \
  missing-core-revision \
  missing-core-latest \
  mismatched-core-tag; do
  printf '%s\n' "${artifact_mode}" >"${fake_gcloud_mode}"
  expect_fail \
    "artifact fixture ${artifact_mode}" \
    "${script_dir}/assert-workload-artifacts.sh" \
    monad-code us-east4 e2b- "${revision}" "${job_binary_bucket}" \
    "${fake_gcloud}"
done
expect_fail \
  "non-SHA image revision" \
  "${script_dir}/assert-workload-artifacts.sh" \
  monad-code us-east4 e2b- latest "${job_binary_bucket}" "${fake_gcloud}"

fake_terraform="${test_dir}/terraform"
terraform_version_file="${test_dir}/terraform-version"
printf '1.7.5\n' >"${terraform_version_file}"
cat >"${fake_terraform}" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
if [[ "${1:-}" == "version" && "${2:-}" == "-json" ]]; then
  jq -cn \
    --arg version "$(cat "${FAKE_TERRAFORM_VERSION_FILE}")" \
    '{terraform_version: $version}'
  exit 0
fi
printf 'unexpected fake Terraform command: %s\n' "$*" >&2
exit 2
EOF
chmod 0755 "${fake_terraform}"
export FAKE_TERRAFORM_VERSION_FILE="${terraform_version_file}"

config_root="${test_dir}/config"
mkdir -p "${config_root}/scripts" "${config_root}/topology" \
  "${config_root}/nomad-cluster-disk-image"
cat >"${config_root}/main.tf" <<'EOF'
terraform {
  required_version = "=1.7.5"
}
EOF
printf 'provider lock\n' >"${config_root}/.terraform.lock.hcl"
printf 'guard\n' >"${config_root}/scripts/guard.sh"
printf 'validator\n' >"${config_root}/scripts/validator.jq"
printf 'make\n' >"${config_root}/Makefile"
cp "${policy}" "${config_root}/topology/policy.json"
cp \
  "${provider_root}/nomad-cluster-disk-image/main.pkr.hcl" \
  "${config_root}/nomad-cluster-disk-image/main.pkr.hcl"
printf 'variable "fixture" { default = "reviewed" }\n' \
  >"${config_root}/nomad-cluster-disk-image/variables.pkr.hcl"
metadata_repo_root="${test_dir}/metadata-repo"
mkdir -p \
  "${metadata_repo_root}/iac/modules/job-fixture/jobs" \
  "${metadata_repo_root}/iac/nomad-cluster-disk-image/setup"
printf 'terraform 1.7.5\n' >"${metadata_repo_root}/.tool-versions"
printf 'resource "fixture" "module" {}\n' \
  >"${metadata_repo_root}/iac/modules/job-fixture/main.tf"
printf 'job "fixture" {}\n' \
  >"${metadata_repo_root}/iac/modules/job-fixture/jobs/job.hcl"
printf 'setup\n' \
  >"${metadata_repo_root}/iac/nomad-cluster-disk-image/setup/setup.sh"
(
  cd "${metadata_repo_root}"
  git init -q
  git config user.name fixture
  git config user.email fixture@example.invalid
  git add .
  git commit -qm fixture
)
environment_file="${test_dir}/.env.dev"
var_file="${config_root}/.terraform.dev.tfvars"
printf 'GCP_PROJECT_ID=monad-code\n' >"${environment_file}"
printf 'api_cluster_size = 1\n' >"${var_file}"
plan="${test_dir}/workload.plan"
manifest="${test_dir}/workload.plan.manifest"
printf 'reviewed saved workload bytes\n' >"${plan}"
chmod 0600 "${plan}"

export WORKLOAD_ENV="dev"
export WORKLOAD_ENV_FILE="${environment_file}"
export WORKLOAD_TF_VAR_FILE="${var_file}"
export WORKLOAD_GCP_PROJECT_ID="monad-code"
export WORKLOAD_GCP_REGION="us-east4"
export WORKLOAD_GCP_ZONE="us-east4-a"
export WORKLOAD_PREFIX="e2b-"
export WORKLOAD_CORE_IMAGE_REVISION="${revision}"
export WORKLOAD_JOB_BINARY_BUCKET="${job_binary_bucket}"
export WORKLOAD_STATE_BUCKET="monad-code-terraform-state"
export WORKLOAD_STATE_PREFIX="terraform/orchestration/dev/state"
export WORKLOAD_TOPOLOGY_POLICY="${config_root}/topology/policy.json"
export WORKLOAD_PACKER_TEMPLATE="${config_root}/nomad-cluster-disk-image/main.pkr.hcl"
artifacts_file="${test_dir}/artifacts.json"
printf 'pass\n' >"${fake_gcloud_mode}"
"${script_dir}/assert-workload-artifacts.sh" \
  monad-code us-east4 e2b- "${revision}" "${job_binary_bucket}" \
  "${fake_gcloud}" \
  >"${artifacts_file}"
chmod 0600 "${artifacts_file}"

fingerprint="$(
  "${script_dir}/workload-plan-metadata.sh" \
    fingerprint "${fake_terraform}" "${config_root}" "${metadata_repo_root}" \
    "${artifacts_file}"
)"
expect_fail \
  "stale fingerprint cannot be recorded" \
  "${script_dir}/workload-plan-metadata.sh" \
  write "${plan}" "${manifest}" "${fake_terraform}" \
  "${config_root}" "${metadata_repo_root}" "${artifacts_file}" deadbeef
expect_pass \
  "workload provenance write" \
  "${script_dir}/workload-plan-metadata.sh" \
  write "${plan}" "${manifest}" "${fake_terraform}" \
  "${config_root}" "${metadata_repo_root}" "${artifacts_file}" "${fingerprint}"
test "$(stat -c '%a' "${manifest}" 2>/dev/null || stat -f '%Lp' "${manifest}")" = "600"
jq -e \
  --arg plan_sha256 "$(shasum -a 256 "${plan}" | awk '{print $1}')" \
  --arg git_head "$(git -C "${metadata_repo_root}" rev-parse HEAD)" \
  '
    .schema_version == 1
    and .plan_sha256 == $plan_sha256
    and .git_head == $git_head
    and .environment == "dev"
    and .gcp_project_id == "monad-code"
    and .gcp_region == "us-east4"
    and .gcp_zone == "us-east4-a"
    and .prefix == "e2b-"
    and .core_image_revision == "0123456789ab"
    and .job_binary_bucket == "monad-code-fc-env-pipeline"
    and .terraform_version == "1.7.5"
    and .backend == {
      type: "gcs",
      bucket: "monad-code-terraform-state",
      prefix: "terraform/orchestration/dev/state",
      workspace: "default"
    }
    and (.source_sha256 | test("^[0-9a-f]{64}$"))
    and (.terraform_lock_sha256 | test("^[0-9a-f]{64}$"))
    and (.environment_file_sha256 | test("^[0-9a-f]{64}$"))
    and (.terraform_var_file_sha256 | test("^[0-9a-f]{64}$"))
    and (.topology_policy_sha256 | test("^[0-9a-f]{64}$"))
    and (.packer_template_sha256 | test("^[0-9a-f]{64}$"))
    and (.packer_inputs_sha256 | test("^[0-9a-f]{64}$"))
    and (.release_artifacts_sha256 | test("^[0-9a-f]{64}$"))
    and .release_artifacts.core_image_revision == "0123456789ab"
    and .release_artifacts.schema_version == 2
    and .release_artifacts.job_binary_bucket == "monad-code-fc-env-pipeline"
    and .release_artifacts.orchestrator_image.family == "e2b-orch"
    and (.release_artifacts.core_images | length) == 5
    and (.release_artifacts.job_binaries | length) == 3
  ' "${manifest}" >/dev/null
expect_pass \
  "unchanged workload provenance" \
  "${script_dir}/workload-plan-metadata.sh" \
  verify "${plan}" "${manifest}" "${fake_terraform}" \
  "${config_root}" "${metadata_repo_root}" "${artifacts_file}"

for identity_mode in different-orchestrator-image different-core-digest; do
  drifted_artifacts="${test_dir}/artifacts-${identity_mode}.json"
  printf '%s\n' "${identity_mode}" >"${fake_gcloud_mode}"
  "${script_dir}/assert-workload-artifacts.sh" \
    monad-code us-east4 e2b- "${revision}" "${job_binary_bucket}" \
    "${fake_gcloud}" \
    >"${drifted_artifacts}"
  chmod 0600 "${drifted_artifacts}"
  expect_fail \
    "live artifact identity drift ${identity_mode}" \
    "${script_dir}/workload-plan-metadata.sh" \
    verify "${plan}" "${manifest}" "${fake_terraform}" \
    "${config_root}" "${metadata_repo_root}" "${drifted_artifacts}"
done
printf 'pass\n' >"${fake_gcloud_mode}"

WORKLOAD_GCP_PROJECT_ID="other-project" \
  expect_fail \
  "project drift invalidates workload plan" \
  "${script_dir}/workload-plan-metadata.sh" \
  verify "${plan}" "${manifest}" "${fake_terraform}" \
  "${config_root}" "${metadata_repo_root}" "${artifacts_file}"

printf '# policy drift\n' >>"${WORKLOAD_TOPOLOGY_POLICY}"
expect_fail \
  "topology policy drift invalidates workload plan" \
  "${script_dir}/workload-plan-metadata.sh" \
  verify "${plan}" "${manifest}" "${fake_terraform}" \
  "${config_root}" "${metadata_repo_root}" "${artifacts_file}"
cp "${policy}" "${WORKLOAD_TOPOLOGY_POLICY}"

printf '# packer drift\n' >>"${WORKLOAD_PACKER_TEMPLATE}"
expect_fail \
  "Packer template drift invalidates workload plan" \
  "${script_dir}/workload-plan-metadata.sh" \
  verify "${plan}" "${manifest}" "${fake_terraform}" \
  "${config_root}" "${metadata_repo_root}" "${artifacts_file}"
cp \
  "${provider_root}/nomad-cluster-disk-image/main.pkr.hcl" \
  "${WORKLOAD_PACKER_TEMPLATE}"

printf '# secondary Packer input drift\n' \
  >>"${config_root}/nomad-cluster-disk-image/variables.pkr.hcl"
expect_fail \
  "secondary Packer input drift invalidates workload plan" \
  "${script_dir}/workload-plan-metadata.sh" \
  verify "${plan}" "${manifest}" "${fake_terraform}" \
  "${config_root}" "${metadata_repo_root}" "${artifacts_file}"
printf 'variable "fixture" { default = "reviewed" }\n' \
  >"${config_root}/nomad-cluster-disk-image/variables.pkr.hcl"

printf '# source drift\n' >>"${config_root}/main.tf"
expect_fail \
  "Terraform source drift invalidates workload plan" \
  "${script_dir}/workload-plan-metadata.sh" \
  verify "${plan}" "${manifest}" "${fake_terraform}" \
  "${config_root}" "${metadata_repo_root}" "${artifacts_file}"
cat >"${config_root}/main.tf" <<'EOF'
terraform {
  required_version = "=1.7.5"
}
EOF

printf '# validator drift\n' >>"${config_root}/scripts/validator.jq"
expect_fail \
  "non-shell validator drift invalidates workload plan" \
  "${script_dir}/workload-plan-metadata.sh" \
  verify "${plan}" "${manifest}" "${fake_terraform}" \
  "${config_root}" "${metadata_repo_root}" "${artifacts_file}"
printf 'validator\n' >"${config_root}/scripts/validator.jq"

printf '# local module drift\n' \
  >>"${metadata_repo_root}/iac/modules/job-fixture/main.tf"
expect_fail \
  "external local Terraform module drift invalidates workload plan" \
  "${script_dir}/workload-plan-metadata.sh" \
  verify "${plan}" "${manifest}" "${fake_terraform}" \
  "${config_root}" "${metadata_repo_root}" "${artifacts_file}"
printf 'resource "fixture" "module" {}\n' \
  >"${metadata_repo_root}/iac/modules/job-fixture/main.tf"

printf '# local module jobspec drift\n' \
  >>"${metadata_repo_root}/iac/modules/job-fixture/jobs/job.hcl"
expect_fail \
  "external local module jobspec drift invalidates workload plan" \
  "${script_dir}/workload-plan-metadata.sh" \
  verify "${plan}" "${manifest}" "${fake_terraform}" \
  "${config_root}" "${metadata_repo_root}" "${artifacts_file}"
printf 'job "fixture" {}\n' \
  >"${metadata_repo_root}/iac/modules/job-fixture/jobs/job.hcl"

stable_fingerprint="$(
  "${script_dir}/workload-plan-metadata.sh" \
    fingerprint "${fake_terraform}" "${config_root}" "${metadata_repo_root}" \
    "${artifacts_file}"
)"
mkdir -p \
  "${config_root}/.workload-plan.dev.fixture" \
  "${config_root}/.workload-apply.dev.fixture"
printf 'generated before\n' \
  >"${config_root}/.workload-plan.dev.fixture/artifacts-before.json"
printf 'generated apply\n' \
  >"${config_root}/.workload-apply.dev.fixture/reviewed.plan"
generated_fingerprint="$(
  "${script_dir}/workload-plan-metadata.sh" \
    fingerprint "${fake_terraform}" "${config_root}" "${metadata_repo_root}" \
    "${artifacts_file}"
)"
test "${generated_fingerprint}" = "${stable_fingerprint}"
rm -rf -- \
  "${config_root}/.workload-plan.dev.fixture" \
  "${config_root}/.workload-apply.dev.fixture"

printf 'tampered\n' >>"${plan}"
expect_fail \
  "plan-byte drift invalidates workload plan" \
  "${script_dir}/workload-plan-metadata.sh" \
  verify "${plan}" "${manifest}" "${fake_terraform}" \
  "${config_root}" "${metadata_repo_root}" "${artifacts_file}"
printf 'reviewed saved workload bytes\n' >"${plan}"
chmod 0644 "${plan}"
expect_fail \
  "world-readable plan is rejected" \
  "${script_dir}/workload-plan-metadata.sh" \
  verify "${plan}" "${manifest}" "${fake_terraform}" \
  "${config_root}" "${metadata_repo_root}" "${artifacts_file}"

workflow_repo="${test_dir}/workflow-repo"
workflow_provider="${workflow_repo}/iac/provider-gcp"
workflow_mode="${test_dir}/workflow-mode"
workflow_terraform_log="${test_dir}/workflow-terraform-log"
workflow_lease_log="${test_dir}/workflow-lease-log"
workflow_make_log="${test_dir}/workflow-make-log"
mkdir -p \
  "${workflow_provider}/scripts/testdata" \
  "${workflow_provider}/topology" \
  "${workflow_provider}/nomad-cluster-disk-image" \
  "${workflow_repo}/iac/modules/job-fixture" \
  "${workflow_repo}/iac/nomad-cluster-disk-image/setup"
cp "${provider_root}/Makefile" "${workflow_provider}/Makefile"
cp "${provider_root}/scripts/assert-workload-artifacts.sh" \
  "${workflow_provider}/scripts/"
cp "${provider_root}/scripts/assert-workload-plan.sh" \
  "${workflow_provider}/scripts/"
cp "${provider_root}/scripts/assert-workload-quota.sh" \
  "${workflow_provider}/scripts/"
cp "${provider_root}/scripts/assert-packer-reserve.sh" \
  "${workflow_provider}/scripts/"
cp "${provider_root}/scripts/workload-plan-metadata.sh" \
  "${workflow_provider}/scripts/"
cp "${provider_root}/scripts/workload-plan-topology.jq" \
  "${workflow_provider}/scripts/"
cp "${provider_root}/scripts/workload-plan-artifacts.jq" \
  "${workflow_provider}/scripts/"
cp "${policy}" "${workflow_provider}/topology/minimal-workload-policy.json"
cp "${provider_root}/nomad-cluster-disk-image/main.pkr.hcl" \
  "${workflow_provider}/nomad-cluster-disk-image/main.pkr.hcl"
cp "${provider_root}/nomad-cluster-disk-image/variables.pkr.hcl" \
  "${workflow_provider}/nomad-cluster-disk-image/variables.pkr.hcl"
cp -R "${repo_root}/iac/nomad-cluster-disk-image/setup/." \
  "${workflow_repo}/iac/nomad-cluster-disk-image/setup/"
printf 'resource "fixture" "module" {}\n' \
  >"${workflow_repo}/iac/modules/job-fixture/main.tf"
jq \
  --slurpfile cloud_sql \
  "${provider_root}/scripts/testdata/cloud-sql-workload-resources.json" \
  --slurpfile artifact_bindings \
  "${provider_root}/scripts/testdata/workload-artifact-plan-bindings.json" '
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
  | .resource_changes |= map(
      if (
        (.address | startswith("module.cluster."))
        and (.type | startswith("google_compute_"))
      )
      then .change = (
        .change
        | .before = .after
        | .actions = ["no-op"]
      )
      else .
      end
    )
  ' \
  "${provider_root}/scripts/testdata/minimal-workload-plan.json" \
  >"${test_dir}/workflow-plan.json"
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
' "${test_dir}/workflow-plan.json" \
  >"${test_dir}/workflow-cluster-plan.json"
printf 'terraform fixture\n' >"${workflow_provider}/main.tf"
printf 'provider lock\n' >"${workflow_provider}/.terraform.lock.hcl"
printf 'api_cluster_size = 1\n' \
  >"${workflow_provider}/.terraform.dev.tfvars"
printf 'dev\n' >"${workflow_repo}/.last_used_env"
cat >"${workflow_repo}/.env.dev" <<EOF
GCP_PROJECT_ID=monad-code
GCP_REGION=us-east4
GCP_ZONE=us-east4-a
PREFIX=e2b-
CORE_IMAGE_REVISION=${revision}
TERRAFORM_ENVIRONMENT=dev
TERRAFORM_STATE_BUCKET=monad-code-terraform-state
EOF
printf 'terraform 1.7.5\n' >"${workflow_repo}/.tool-versions"
printf 'pass\n' >"${workflow_mode}"
: >"${workflow_terraform_log}"
: >"${workflow_lease_log}"
: >"${workflow_make_log}"

workflow_terraform="${test_dir}/workflow-terraform"
cat >"${workflow_terraform}" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >>"${WORKFLOW_TERRAFORM_LOG}"
mode="$(cat "${WORKFLOW_MODE_FILE}")"
case "${1:-}" in
  version)
    printf '%s\n' '{"terraform_version":"1.7.5"}'
    ;;
  plan)
    output=""
    detailed=false
    for argument in "$@"; do
      case "${argument}" in
        -out=*) output="${argument#-out=}" ;;
        -detailed-exitcode) detailed=true ;;
      esac
    done
    [[ -n "${output}" ]] || {
      printf 'fake Terraform plan requires -out\n' >&2
      exit 2
    }
    if [[ "${mode}" == "plan-fail" && "${detailed}" == false ]]; then
      exit 1
    fi
    cp "${WORKFLOW_PLAN_FIXTURE}" "${output}"
    if [[ "${mode}" == "post-drift" && "${detailed}" == true ]]; then
      exit 2
    fi
    ;;
  show)
    [[ "${2:-}" == "-json" ]] || exit 2
    cat "${3:?missing saved plan}"
    ;;
  apply)
    [[ "${2:-}" == "-input=false" ]] || exit 2
    [[ -f "${3:-}" ]] || exit 2
    [[ "${3:-}" != "${WORKFLOW_SHARED_PLAN}" ]] || {
      printf 'apply used mutable published plan path\n' >&2
      exit 2
    }
    if [[ "${mode}" == "replace-shared-during-apply" ]]; then
      printf 'replacement plan\n' >"${WORKFLOW_SHARED_PLAN}"
      printf 'replacement manifest\n' >"${WORKFLOW_SHARED_MANIFEST}"
      chmod 0600 "${WORKFLOW_SHARED_PLAN}" "${WORKFLOW_SHARED_MANIFEST}"
    fi
    [[ "${mode}" != "apply-fail" ]] || exit 1
    ;;
  *)
    printf 'unexpected fake Terraform command: %s\n' "$*" >&2
    exit 2
    ;;
esac
EOF
chmod 0755 "${workflow_terraform}"

workflow_make="${test_dir}/workflow-make"
cat >"${workflow_make}" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >>"${WORKFLOW_MAKE_LOG}"
[[ "$*" == *"workload-context-guard"* ]] || {
  printf 'unexpected fake recursive make: %s\n' "$*" >&2
  exit 2
}
EOF
chmod 0755 "${workflow_make}"

cat >"${workflow_provider}/scripts/rollout-mutation-lease.sh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >>"${WORKFLOW_LEASE_LOG}"
case "${1:-}" in
  acquire)
    [[ "$(cat "${WORKFLOW_MODE_FILE}")" != "lease-acquire-fail" ]] || exit 1
    token="${7:?missing token}"
    umask 077
    printf '%s\n' '{"fixture":"lease"}' >"${token}"
    chmod 0600 "${token}"
    ;;
  release)
    [[ "$(cat "${WORKFLOW_MODE_FILE}")" != "release-fail" ]] || exit 1
    rm -f -- "${3:?missing token}"
    ;;
  *)
    exit 2
    ;;
esac
EOF
chmod 0755 "${workflow_provider}/scripts/"*.sh

(
  cd "${workflow_repo}"
  git init -q
  git config user.name fixture
  git config user.email fixture@example.invalid
  git add .
  git commit -qm fixture
)

export WORKFLOW_MODE_FILE="${workflow_mode}"
export WORKFLOW_TERRAFORM_LOG="${workflow_terraform_log}"
export WORKFLOW_LEASE_LOG="${workflow_lease_log}"
export WORKFLOW_MAKE_LOG="${workflow_make_log}"
export WORKFLOW_PLAN_FIXTURE="${test_dir}/workflow-plan.json"

run_workflow_make() {
  make \
    -C "${workflow_provider}" \
    ENV=dev \
    TF="${workflow_terraform}" \
    GCLOUD="${fake_gcloud}" \
    MAKE="${workflow_make}" \
    SKIP_FMT=true \
    "$@"
}

workflow_plan="${workflow_provider}/.tfplan.workload.dev"
workflow_manifest="${workflow_plan}.manifest"
export WORKFLOW_SHARED_PLAN="${workflow_plan}"
export WORKFLOW_SHARED_MANIFEST="${workflow_manifest}"
printf 'stale plan\n' >"${workflow_plan}"
printf 'stale manifest\n' >"${workflow_manifest}"
chmod 0600 "${workflow_plan}" "${workflow_manifest}"
printf 'lease-acquire-fail\n' >"${workflow_mode}"
expect_fail "failed plan lease preserves the previously reviewed release" \
  run_workflow_make workload-plan
test "$(cat "${workflow_plan}")" = "stale plan"
test "$(cat "${workflow_manifest}")" = "stale manifest"

printf 'plan-fail\n' >"${workflow_mode}"
expect_fail "failed plan invalidates old plan and manifest" \
  run_workflow_make workload-plan
test ! -e "${workflow_plan}"
test ! -e "${workflow_manifest}"

printf 'pass\n' >"${workflow_mode}"
expect_pass "full workload plan publishes private reviewed bytes" \
  run_workflow_make workload-plan
test -f "${workflow_plan}"
test -f "${workflow_manifest}"
test "$(stat -c '%a' "${workflow_plan}" 2>/dev/null || stat -f '%Lp' "${workflow_plan}")" = "600"
test "$(stat -c '%a' "${workflow_manifest}" 2>/dev/null || stat -f '%Lp' "${workflow_manifest}")" = "600"
test -z "$(find "${workflow_provider}" -maxdepth 1 -type d -name '.workload-plan.*' -print)"
test "$(grep -c '^acquire ' "${workflow_lease_log}")" -eq 3
test "$(grep -c '^release ' "${workflow_lease_log}")" -eq 2
: >"${workflow_lease_log}"
initial_plan_command="$(
  grep '^plan ' "${workflow_terraform_log}" | head -1
)"
if grep -Eq -- '(^|[[:space:]])(-target|-destroy)(=|[[:space:]])' \
  <<<"${initial_plan_command}"; then
  printf 'workflow fixture observed a partial or destroy plan\n' >&2
  exit 1
fi

expect_fail "wrong apply confirmation preserves saved release" \
  run_workflow_make workload-apply CONFIRM=wrong
test -f "${workflow_plan}"
test -f "${workflow_manifest}"

printf 'apply-fail\n' >"${workflow_mode}"
expect_fail "Terraform apply failure preserves reviewed release" \
  run_workflow_make workload-apply CONFIRM='APPLY ONE WORKCELL CANARY'
test -f "${workflow_plan}"
test -f "${workflow_manifest}"
test "$(grep -c '^acquire ' "${workflow_lease_log}")" -eq 1
test "$(grep -c '^release ' "${workflow_lease_log}")" -eq 1

printf 'post-drift\n' >"${workflow_mode}"
expect_fail "post-apply drift preserves reviewed release" \
  run_workflow_make workload-apply CONFIRM='APPLY ONE WORKCELL CANARY'
test -f "${workflow_plan}"
test -f "${workflow_manifest}"
test "$(grep -c '^acquire ' "${workflow_lease_log}")" -eq 2
test "$(grep -c '^release ' "${workflow_lease_log}")" -eq 2

printf 'replace-shared-during-apply\n' >"${workflow_mode}"
expect_fail "concurrent published-plan replacement is preserved" \
  run_workflow_make workload-apply CONFIRM='APPLY ONE WORKCELL CANARY'
test "$(cat "${workflow_plan}")" = "replacement plan"
test "$(cat "${workflow_manifest}")" = "replacement manifest"
test "$(grep -c '^acquire ' "${workflow_lease_log}")" -eq 3
test "$(grep -c '^release ' "${workflow_lease_log}")" -eq 3

printf 'pass\n' >"${workflow_mode}"
expect_pass "replacement can be superseded by a newly reviewed plan" \
  run_workflow_make workload-plan
: >"${workflow_lease_log}"

printf 'release-fail\n' >"${workflow_mode}"
expect_fail "lease release failure restores reviewed evidence" \
  run_workflow_make workload-apply CONFIRM='APPLY ONE WORKCELL CANARY'
test -f "${workflow_plan}"
test -f "${workflow_manifest}"
test "$(grep -c '^acquire ' "${workflow_lease_log}")" -eq 1
test "$(grep -c '^release ' "${workflow_lease_log}")" -eq 2

printf 'pass\n' >"${workflow_mode}"
: >"${workflow_lease_log}"
expect_pass "clean saved-plan apply consumes release exactly once" \
  run_workflow_make workload-apply CONFIRM='APPLY ONE WORKCELL CANARY'
test ! -e "${workflow_plan}"
test ! -e "${workflow_manifest}"
test "$(grep -c '^acquire ' "${workflow_lease_log}")" -eq 1
test "$(grep -c '^release ' "${workflow_lease_log}")" -eq 1
test -z "$(find "${workflow_provider}" -maxdepth 1 -type d -name '.workload-apply.*' -print)"

workflow_cluster_plan="${workflow_provider}/.tfplan.workload-cluster.dev"
workflow_cluster_manifest="${workflow_cluster_plan}.manifest"
export WORKFLOW_PLAN_FIXTURE="${test_dir}/workflow-cluster-plan.json"
export WORKFLOW_SHARED_PLAN="${workflow_cluster_plan}"
export WORKFLOW_SHARED_MANIFEST="${workflow_cluster_manifest}"
: >"${workflow_terraform_log}"
: >"${workflow_lease_log}"
printf 'stale cluster plan\n' >"${workflow_cluster_plan}"
printf 'stale cluster manifest\n' >"${workflow_cluster_manifest}"
chmod 0600 "${workflow_cluster_plan}" "${workflow_cluster_manifest}"

printf 'lease-acquire-fail\n' >"${workflow_mode}"
expect_fail "failed cluster-plan lease preserves the previously reviewed release" \
  run_workflow_make workload-cluster-plan
test "$(cat "${workflow_cluster_plan}")" = "stale cluster plan"
test "$(cat "${workflow_cluster_manifest}")" = "stale cluster manifest"

printf 'plan-fail\n' >"${workflow_mode}"
expect_fail "failed cluster plan invalidates old plan and manifest" \
  run_workflow_make workload-cluster-plan
test ! -e "${workflow_cluster_plan}"
test ! -e "${workflow_cluster_manifest}"

printf 'pass\n' >"${workflow_mode}"
expect_pass "cluster-only plan publishes private reviewed bytes" \
  run_workflow_make workload-cluster-plan
test -f "${workflow_cluster_plan}"
test -f "${workflow_cluster_manifest}"
test "$(stat -c '%a' "${workflow_cluster_plan}" 2>/dev/null || stat -f '%Lp' "${workflow_cluster_plan}")" = "600"
test "$(stat -c '%a' "${workflow_cluster_manifest}" 2>/dev/null || stat -f '%Lp' "${workflow_cluster_manifest}")" = "600"
test -z "$(find "${workflow_provider}" -maxdepth 1 -type d -name '.workload-cluster-plan.*' -print)"
test "$(grep -c '^acquire ' "${workflow_lease_log}")" -eq 3
test "$(grep -c '^release ' "${workflow_lease_log}")" -eq 2
cluster_initial_plan_command="$(
  grep '^plan ' "${workflow_terraform_log}" | head -1
)"
grep -F -- '-target=module.cluster' \
  <<<"${cluster_initial_plan_command}" >/dev/null
if grep -Eq -- '(^|[[:space:]])-destroy(=|[[:space:]]|$)' \
  <<<"${cluster_initial_plan_command}"; then
  printf 'cluster workflow fixture observed a destroy plan\n' >&2
  exit 1
fi

expect_fail "wrong cluster apply confirmation preserves saved release" \
  run_workflow_make workload-cluster-apply CONFIRM=wrong
test -f "${workflow_cluster_plan}"
test -f "${workflow_cluster_manifest}"

: >"${workflow_lease_log}"
printf 'apply-fail\n' >"${workflow_mode}"
expect_fail "cluster Terraform apply failure preserves reviewed release" \
  run_workflow_make \
  workload-cluster-apply CONFIRM='APPLY ONE WORKCELL CLUSTER'
test -f "${workflow_cluster_plan}"
test -f "${workflow_cluster_manifest}"
test "$(grep -c '^acquire ' "${workflow_lease_log}")" -eq 1
test "$(grep -c '^release ' "${workflow_lease_log}")" -eq 1

printf 'post-drift\n' >"${workflow_mode}"
expect_fail "post-apply cluster drift preserves reviewed release" \
  run_workflow_make \
  workload-cluster-apply CONFIRM='APPLY ONE WORKCELL CLUSTER'
test -f "${workflow_cluster_plan}"
test -f "${workflow_cluster_manifest}"
test "$(grep -c '^acquire ' "${workflow_lease_log}")" -eq 2
test "$(grep -c '^release ' "${workflow_lease_log}")" -eq 2

printf 'pass\n' >"${workflow_mode}"
: >"${workflow_lease_log}"
expect_pass "clean saved cluster-plan apply consumes release exactly once" \
  run_workflow_make \
  workload-cluster-apply CONFIRM='APPLY ONE WORKCELL CLUSTER'
test ! -e "${workflow_cluster_plan}"
test ! -e "${workflow_cluster_manifest}"
test "$(grep -c '^acquire ' "${workflow_lease_log}")" -eq 1
test "$(grep -c '^release ' "${workflow_lease_log}")" -eq 1
test -z "$(find "${workflow_provider}" -maxdepth 1 -type d -name '.workload-cluster-apply.*' -print)"
cluster_post_apply_plan_command="$(
  grep '^plan ' "${workflow_terraform_log}" | tail -1
)"
grep -F -- '-target=module.cluster' \
  <<<"${cluster_post_apply_plan_command}" >/dev/null
grep -F -- '-detailed-exitcode' \
  <<<"${cluster_post_apply_plan_command}" >/dev/null

cluster_plan_recipe="$(
  awk '
    /^workload-cluster-plan:/ {capture = 1}
    /^workload-cluster-apply:/ {capture = 0}
    capture {print}
  ' "${provider_root}/Makefile"
)"
cluster_apply_recipe="$(
  awk '
    /^workload-cluster-apply:/ {capture = 1}
    /^workload-cluster-wait:/ {capture = 0}
    capture {print}
  ' "${provider_root}/Makefile"
)"
cluster_wait_recipe="$(
  awk '
    /^workload-cluster-wait:/ {capture = 1}
    /^workload-plan:/ {capture = 0}
    capture {print}
  ' "${provider_root}/Makefile"
)"
grep -F 'rm -f -- "$(WORKLOAD_CLUSTER_PLAN)" "$(WORKLOAD_CLUSTER_PLAN_MANIFEST)"' \
  <<<"${cluster_plan_recipe}" >/dev/null
grep -F -- '-target=module.cluster' <<<"${cluster_plan_recipe}" >/dev/null
grep -F '"$${after_artifacts}" cluster' <<<"${cluster_plan_recipe}" >/dev/null
grep -F '"$(WORKLOAD_ROLLOUT_LEASE)" acquire' \
  <<<"${cluster_plan_recipe}" >/dev/null
cluster_plan_acquire_line="$(
  grep -nF '"$(WORKLOAD_ROLLOUT_LEASE)" acquire' \
    <<<"${cluster_plan_recipe}" | cut -d: -f1
)"
cluster_plan_remove_line="$(
  grep -nF 'rm -f -- "$(WORKLOAD_CLUSTER_PLAN)" "$(WORKLOAD_CLUSTER_PLAN_MANIFEST)"' \
    <<<"${cluster_plan_recipe}" | cut -d: -f1
)"
test "${cluster_plan_acquire_line}" -lt "${cluster_plan_remove_line}"
grep -F '$(WORKLOAD_CLUSTER_CONFIRMATION)' \
  <<<"${cluster_apply_recipe}" >/dev/null
grep -F 'cp "$(WORKLOAD_CLUSTER_PLAN)" "$${apply_plan}"' \
  <<<"${cluster_apply_recipe}" >/dev/null
grep -F '"$${before_artifacts}" cluster' \
  <<<"${cluster_apply_recipe}" >/dev/null
grep -F '$(TF) apply -input=false "$${apply_plan}"' \
  <<<"${cluster_apply_recipe}" >/dev/null
grep -F -- '-target=module.cluster' <<<"${cluster_apply_recipe}" >/dev/null
grep -F -- '-detailed-exitcode' <<<"${cluster_apply_recipe}" >/dev/null
grep -F './scripts/wait-workload-cluster.sh' \
  <<<"${cluster_wait_recipe}" >/dev/null

workload_plan_recipe="$(
  awk '
    /^workload-plan:/ {capture = 1}
    /^workload-apply:/ {capture = 0}
    capture {print}
  ' "${provider_root}/Makefile"
)"
workload_apply_recipe="$(
  awk '
    /^workload-apply:/ {capture = 1}
    /^\\.PHONY: foundation-init/ {capture = 0}
    capture {print}
  ' "${provider_root}/Makefile"
)"
grep -F 'rm -f -- "$(WORKLOAD_PLAN)" "$(WORKLOAD_PLAN_MANIFEST)"' \
  <<<"${workload_plan_recipe}" >/dev/null
grep -F 'mktemp -d ".workload-plan.$(ENV).XXXXXX"' \
  <<<"${workload_plan_recipe}" >/dev/null
grep -F 'chmod 0600 "$${temp_plan}"' <<<"${workload_plan_recipe}" >/dev/null
grep -F './scripts/assert-workload-plan.sh' <<<"${workload_plan_recipe}" >/dev/null
grep -F './scripts/assert-workload-quota.sh' <<<"${workload_plan_recipe}" >/dev/null
grep -F '"$(WORKLOAD_ROLLOUT_LEASE)" acquire' \
  <<<"${workload_plan_recipe}" >/dev/null
grep -F '"$(WORKLOAD_ROLLOUT_LEASE)" release' \
  <<<"${workload_plan_recipe}" >/dev/null
plan_acquire_line="$(
  grep -nF '"$(WORKLOAD_ROLLOUT_LEASE)" acquire' \
    <<<"${workload_plan_recipe}" | cut -d: -f1
)"
plan_remove_line="$(
  grep -nF 'rm -f -- "$(WORKLOAD_PLAN)" "$(WORKLOAD_PLAN_MANIFEST)"' \
    <<<"${workload_plan_recipe}" | cut -d: -f1
)"
plan_final_release_line="$(
  grep -nF '"$(WORKLOAD_ROLLOUT_LEASE)" release' \
    <<<"${workload_plan_recipe}" | tail -1 | cut -d: -f1
)"
test "${plan_acquire_line}" -lt "${plan_remove_line}"
test "${plan_remove_line}" -lt "${plan_final_release_line}"
if grep -E -- '(^|[[:space:]])(-target|-destroy)(=|[[:space:]])' \
  <<<"${workload_plan_recipe}" >/dev/null; then
  printf 'workload-plan must remain a full, non-destroy Terraform plan\n' >&2
  exit 1
fi
grep -F 'CONFIRM' <<<"${workload_apply_recipe}" \
  | grep -F '$(WORKLOAD_CONFIRMATION)' >/dev/null
grep -F '$(TF) apply -input=false "$${apply_plan}"' \
  <<<"${workload_apply_recipe}" >/dev/null
test "$(
  grep -Fc './scripts/workload-plan-metadata.sh verify' \
    <<<"${workload_apply_recipe}"
)" -eq 3
grep -F 'cp "$(WORKLOAD_PLAN)" "$${apply_plan}"' \
  <<<"${workload_apply_recipe}" >/dev/null
grep -F 'cmp -s "$(WORKLOAD_PLAN)" "$${apply_plan}"' \
  <<<"${workload_apply_recipe}" >/dev/null
grep -F './scripts/assert-workload-plan.sh' <<<"${workload_apply_recipe}" >/dev/null
grep -F './scripts/assert-workload-quota.sh' <<<"${workload_apply_recipe}" >/dev/null
grep -F './scripts/assert-workload-artifacts.sh' <<<"${workload_apply_recipe}" >/dev/null
grep -F '"$(WORKLOAD_ROLLOUT_LEASE)" acquire' \
  <<<"${workload_apply_recipe}" >/dev/null
grep -F '"$(WORKLOAD_ROLLOUT_LEASE)" release' \
  <<<"${workload_apply_recipe}" >/dev/null
grep -F -- '-detailed-exitcode' <<<"${workload_apply_recipe}" >/dev/null
grep -F 'mv "$(WORKLOAD_PLAN)" "$${consumed_plan}"' \
  <<<"${workload_apply_recipe}" >/dev/null
apply_acquire_line="$(
  grep -nF '"$(WORKLOAD_ROLLOUT_LEASE)" acquire' \
    <<<"${workload_apply_recipe}" | cut -d: -f1
)"
apply_first_artifact_check_line="$(
  grep -nF './scripts/assert-workload-artifacts.sh' \
    <<<"${workload_apply_recipe}" | head -1 | cut -d: -f1
)"
apply_move_line="$(
  grep -nF 'mv "$(WORKLOAD_PLAN)" "$${consumed_plan}"' \
    <<<"${workload_apply_recipe}" | cut -d: -f1
)"
apply_final_release_line="$(
  grep -nF '"$(WORKLOAD_ROLLOUT_LEASE)" release' \
    <<<"${workload_apply_recipe}" | tail -1 | cut -d: -f1
)"
test "${apply_acquire_line}" -lt "${apply_first_artifact_check_line}"
test "${apply_move_line}" -lt "${apply_final_release_line}"
grep -F 'override WORKLOAD_IMAGE_REVISION := $(CORE_IMAGE_REVISION)' \
  "${provider_root}/Makefile" >/dev/null
grep -F '$(call tfvar, CORE_IMAGE_REVISION)' \
  "${provider_root}/Makefile" >/dev/null
grep -F 'CORE_IMAGE_REVISION=' "${repo_root}/.env.gcp.template" >/dev/null
grep -F 'name   = "orchestrator${local.job_binary_suffix}"' \
  "${provider_root}/nomad/main.tf" >/dev/null
grep -F '#${local.orchestrator_checksum}' \
  "${provider_root}/nomad/main.tf" >/dev/null
test "$(
  grep -c -- '--if-generation-match=0' \
    "${repo_root}/packages/orchestrator/Makefile"
)" -eq 3
test "$(
  grep -c -- "--if-none-match '\\*'" \
    "${repo_root}/packages/orchestrator/Makefile"
)" -eq 3

printf 'Workload release gate tests passed without contacting GCP.\n'
