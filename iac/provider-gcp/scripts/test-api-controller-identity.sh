#!/usr/bin/env bash

set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
provider_root="$(cd "${script_dir}/.." && pwd)"
repo_root="$(cd "${provider_root}/../.." && pwd)"
identity_tf="${provider_root}/init/api-controller-identity.tf"
init_outputs="${provider_root}/init/outputs.tf"
cluster_main="${provider_root}/nomad-cluster/main.tf"
api_pool="${provider_root}/nomad-cluster/nodepool-api.tf"
cluster_variables="${provider_root}/nomad-cluster/variables.tf"
root_main="${provider_root}/main.tf"
root_outputs="${provider_root}/outputs.tf"
api_tf="${provider_root}/api.tf"

grep -F 'resource "google_service_account" "api_controller_service_account"' \
  "${identity_tf}" >/dev/null
grep -F 'roles/storage.objectViewer' "${identity_tf}" >/dev/null
grep -F 'roles/artifactregistry.reader' "${identity_tf}" >/dev/null
grep -F 'roles/logging.logWriter' "${identity_tf}" >/dev/null
grep -F 'roles/monitoring.editor' "${identity_tf}" >/dev/null
grep -F 'roles/compute.networkViewer' "${identity_tf}" >/dev/null
if grep -E 'roles/iam\.serviceAccountTokenCreator|fc_template_bucket|fc_build_cache_bucket|envs_docker_context' \
  "${identity_tf}" >/dev/null; then
  printf 'The API/controller identity gained a worker/build or signing permission.\n' >&2
  exit 1
fi

grep -F 'output "api_controller_service_account_email"' "${init_outputs}" >/dev/null
grep -F 'google_service_account.api_controller_service_account.email' \
  "${init_outputs}" >/dev/null
grep -F 'output "api_controller_service_account_unique_id"' "${init_outputs}" >/dev/null
grep -F 'google_service_account.api_controller_service_account.unique_id' \
  "${init_outputs}" >/dev/null
grep -F 'output "api_controller_service_account_email"' "${root_outputs}" >/dev/null
grep -F 'output "api_controller_service_account_unique_id"' "${root_outputs}" >/dev/null

grep -F 'variable "api_controller_service_account_email"' \
  "${cluster_variables}" >/dev/null
grep -F 'email = var.api_controller_service_account_email' "${api_pool}" >/dev/null
if grep -F 'email = var.google_service_account_email' "${api_pool}" >/dev/null; then
  printf 'The API pool still attaches the shared worker/build identity.\n' >&2
  exit 1
fi
grep -F 'var.api_controller_service_account_email != var.google_service_account_email' \
  "${api_pool}" >/dev/null
grep -F 'var.monad_worker_autoscaler_shadow_enabled' "${api_pool}" >/dev/null

# Build and worker pools deliberately keep the shared runtime identity. The
# distinct identity must never be threaded into either worker-cluster module.
test "$(grep -Fc 'google_service_account_email = var.google_service_account_email' "${cluster_main}")" -eq 2
if grep -F 'api_controller_service_account_email' "${cluster_main}" >/dev/null; then
  printf 'The API/controller identity escaped into a worker or build module.\n' >&2
  exit 1
fi

grep -F 'var.environment == "dev"' "${root_main}" >/dev/null
grep -F '? module.init.api_controller_service_account_email' "${root_main}" >/dev/null
grep -F ': module.init.service_account_email' "${root_main}" >/dev/null
grep -F 'api_controller_service_account_email = (' "${root_main}" >/dev/null

grep -F 'resource "google_artifact_registry_repository_iam_member" "custom_environments_repository_api_controller_member"' \
  "${api_tf}" >/dev/null
grep -F 'module.init.api_controller_service_account_email' "${api_tf}" >/dev/null

for variable in \
  MONAD_WORKER_AUTOSCALER_SHADOW_ENABLED \
  MONAD_WORKER_AUTOSCALER_REVISION \
  MONAD_WORKER_AUTOSCALER_TAMS_CAPACITY_URL \
  MONAD_WORKER_AUTOSCALER_TAMS_AUDIENCE \
  MONAD_WORKER_AUTOSCALER_ALLOCATIONS; do
  grep -F "\$(call tfvar, ${variable})" "${provider_root}/Makefile" >/dev/null
  grep -F "${variable}=" "${repo_root}/.env.gcp.template" >/dev/null
done

grep -F "custom_environments_repository_api_controller_member'" \
  "${provider_root}/Makefile" >/dev/null

printf 'Dedicated API/controller attached-identity guards passed.\n'
