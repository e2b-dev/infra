#!/usr/bin/env bash
set -euo pipefail

terraform_bin="${1:-terraform}"
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
provider_dir="$(cd "${script_dir}/.." && pwd)"
template_path="${provider_dir}/nomad-cluster/scripts/start-client.sh"
nodepool_path="${provider_dir}/nomad-cluster/worker-cluster/nodepool.tf"
cluster_path="${provider_dir}/nomad-cluster/main.tf"
environment_template="${provider_dir}/../../.env.gcp.template"
test_dir="$(mktemp -d)"
trap 'rm -rf -- "${test_dir}"' EXIT

command -v "${terraform_bin}" >/dev/null 2>&1 || {
  printf 'Terraform executable is required to render worker startup: %s\n' \
    "${terraform_bin}" >&2
  exit 1
}

command -v jq >/dev/null 2>&1 || {
  printf 'jq is required to inspect the canary worker configuration.\n' >&2
  exit 1
}

build_config="$(
  sed -n "s/^BUILD_CLUSTERS_CONFIG='\\(.*\\)'$/\\1/p" \
    "${environment_template}"
)"
client_config="$(
  sed -n "s/^CLIENT_CLUSTERS_CONFIG='\\(.*\\)'$/\\1/p" \
    "${environment_template}"
)"

jq -e '
  .default.boot_disk
    == {"disk_type": "pd-ssd", "size_gb": 100, "swap_size_gb": 32}
' <<<"${build_config}" >/dev/null
jq -e '
  .default.boot_disk
    == {"disk_type": "pd-ssd", "size_gb": 100, "swap_size_gb": 32}
' <<<"${client_config}" >/dev/null

canary_swap_size_gb="$(
  jq -r '.default.boot_disk.swap_size_gb' <<<"${client_config}"
)"

render_startup() {
  local swap_size_gb="$1"
  local set_orchestrator_version_metadata="$2"
  local output_path="$3"
  local config_path="${test_dir}/main.tf"

  cat >"${config_path}" <<EOF
locals {
  startup = templatefile("${template_path}", {
    CLUSTER_TAG_NAME = "test-cluster"
    SCRIPTS_BUCKET = "scripts"
    FC_KERNELS_BUCKET_NAME = "kernels"
    FC_VERSIONS_BUCKET_NAME = "versions"
    FC_ENV_PIPELINE_BUCKET_NAME = "envd"
    FC_BUSYBOX_BUCKET_NAME = "busybox"
    DOCKER_CONTEXTS_BUCKET_NAME = "docker"
    GCP_REGION = "us-east4"
    NOMAD_TOKEN = "nomad-token"
    CONSUL_TOKEN = "consul-token"
    CONFIGURE_DOCKER_FILE_HASH = "docker-hash"
    RUN_CONSUL_FILE_HASH = "consul-hash"
    RUN_NOMAD_FILE_HASH = "nomad-hash"
    CONSUL_GOSSIP_ENCRYPTION_KEY = "gossip-key"
    CONSUL_DNS_REQUEST_TOKEN = "dns-token"
    NFS_IP_ADDRESS = ""
    NFS_MOUNT_PATH = "/mnt/nfs"
    NFS_MOUNT_SUBDIR = "cache"
    NFS_MOUNT_OPTS = "defaults"
    USE_FILESTORE_CACHE = false
    NODE_POOL = "test"
    BASE_HUGEPAGES_PERCENTAGE = 80
    CACHE_DISK_COUNT = 1
    LOCAL_SSD = "true"
    SWAP_SIZE_GB = ${swap_size_gb}
    SET_ORCHESTRATOR_VERSION_METADATA = "${set_orchestrator_version_metadata}"
    NODE_LABELS = ""
    PERSISTENT_VOLUME_TYPES = {}
  })
}
EOF

  printf 'jsonencode(local.startup)\n' \
    | "${terraform_bin}" -chdir="${test_dir}" console \
    | jq -r . \
    | jq -r . >"${output_path}"
}

dev_render="${test_dir}/dev-startup.sh"
nondev_render="${test_dir}/nondev-startup.sh"
render_startup "${canary_swap_size_gb}" "false" "${dev_render}"
render_startup 48 "true" "${nondev_render}"

bash -n "${dev_render}"
bash -n "${nondev_render}"

if grep -Eq '^[[:space:]]*set[[:space:]]+-x([[:space:]]|$)' \
  "${template_path}" "${dev_render}" "${nondev_render}"; then
  printf 'Worker bootstrap must not persist rendered ACL material through shell tracing.\n' >&2
  exit 1
fi

grep -F 'readonly SWAP_SIZE_GB=32' "${dev_render}" >/dev/null
grep -F 'readonly SWAP_SIZE_GB=48' "${nondev_render}" >/dev/null
grep -F 'fallocate --length "${SWAP_SIZE_GB}G" "$SWAPFILE"' \
  "${dev_render}" >/dev/null
grep -F 'active swapfile size ${active_swap_size_bytes} does not match configured size ${SWAP_SIZE_BYTES}' \
  "${dev_render}" >/dev/null
grep -F '[[ -L "$SWAPFILE" || ( -e "$SWAPFILE" && ! -f "$SWAPFILE" ) ]]' \
  "${dev_render}" >/dev/null
grep -E 'SWAP_SIZE_GB[[:space:]]*=[[:space:]]*var\.boot_disk\.swap_size_gb' \
  "${nodepool_path}" >/dev/null
grep -F 'set_orchestrator_version_metadata = var.environment != "dev"' \
  "${cluster_path}" >/dev/null

if grep -F '[Fetching orchestrator version from Nomad servers' \
  "${dev_render}" >/dev/null; then
  printf 'Dev worker startup must not wait for phase-two orchestrator metadata.\n' >&2
  exit 1
fi

if grep -F -- '--orchestrator-job-version' "${dev_render}" >/dev/null; then
  printf 'Dev worker startup must not set orchestrator job-version metadata.\n' >&2
  exit 1
fi

grep -F '[Fetching orchestrator version from Nomad servers' \
  "${nondev_render}" >/dev/null
grep -F -- '--orchestrator-job-version "$ORCHESTRATOR_VERSION"' \
  "${nondev_render}" >/dev/null

if grep -F 'fallocate -l 100G' "${dev_render}" >/dev/null; then
  printf 'Worker startup still contains the legacy hard-coded 100 GB swap allocation.\n' >&2
  exit 1
fi

if grep -F -- '--show=NAME,SIZE' "${dev_render}" >/dev/null; then
  printf 'Worker startup must validate the backing file size, not the smaller usable swap area.\n' >&2
  exit 1
fi

printf 'Worker startup swap and orchestrator metadata rendering passed.\n'
