#!/usr/bin/env bash
set -euo pipefail

template="${1:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)/main.pkr.hcl}"
variables="${2:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)/variables.pkr.hcl}"
artifact_lock="${3:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)/setup/root-artifacts.lock.json}"

for path in "${template}" "${variables}" "${artifact_lock}"; do
  [[ -f "${path}" && ! -L "${path}" ]] || {
    printf 'Packer source must be a regular, non-symlink file: %s\n' "${path}" >&2
    exit 1
  }
done

normalized="$(sed -E 's/[[:space:]]+//g' "${template}")"

require_once() {
  local needle="$1"
  local description="$2"
  local expected_count="${3:-1}"
  local count
  count="$(grep -Fxc "${needle}" <<<"${normalized}" || true)"
  [[ "${count}" -eq "${expected_count}" ]] || {
    printf 'Packer template must contain exactly %s %s entries: %s\n' \
      "${expected_count}" "${description}" "${needle}" >&2
    exit 1
  }
}

require_present() {
  local needle="$1"
  local description="$2"
  grep -Fq "${needle}" <<<"${normalized}" || {
    printf 'Packer template must contain %s: %s\n' \
      "${description}" "${needle}" >&2
    exit 1
  }
}

[[ "$(grep -Ec '^source "googlecompute" "orch" \{' "${template}")" -eq 1 ]] || {
  printf 'Packer template must declare exactly one reviewed googlecompute source.\n' >&2
  exit 1
}
[[ "$(grep -Ec '^build \{' "${template}")" -eq 1 ]] || {
  printf 'Packer template must declare exactly one build block.\n' >&2
  exit 1
}

if grep -Eqi '(^|[[:space:]])force[[:space:]]*=' "${template}"; then
  printf 'Packer force replacement is forbidden for operator canaries.\n' >&2
  exit 1
fi
if grep -Eq \
  'get\.docker\.com|add-google-cloud-ops-agent-repo|git clone|CHECKSUM_URL' \
  "${template}"; then
  printf 'Packer template contains an unreviewed mutable root download.\n' >&2
  exit 1
fi

require_once 'required_version="=1.13.1"' 'Packer version pin'
require_once 'version="1.0.16"' 'googlecompute plugin pin'
require_once 'source="github.com/hashicorp/googlecompute"' 'plugin source'
require_once 'source_image_project_id=["ubuntu-os-cloud"]' 'source-image project'
require_once 'disable_default_service_account=true' 'default service-account disablement'
require_once 'image_name=var.image_name' 'deterministic image name' 2
require_once 'image_family=var.image_family' 'candidate image family' 2
require_once 'sources=["source.googlecompute.orch"]' 'reviewed source binding'
require_once 'post-processor"manifest"{' 'build manifest'
require_once 'output=var.build_manifest_path' 'explicit manifest output'
require_once 'root_artifact_lock=jsondecode(file(abspath("${path.root}/setup/root-artifacts.lock.json")))' 'root artifact lock'
require_once 'root_artifact_lock_sha=sha256(file(abspath("${path.root}/setup/root-artifacts.lock.json")))' 'root artifact lock digest'
require_once 'root_input_lock_sha256=local.root_artifact_lock_sha' 'manifest root artifact identity'
require_present 'Snapshot:${local.root_artifact_lock.ubuntu_snapshot}' 'Ubuntu package snapshot'

if ! jq -e '
  .schema_version == 1
  and .ubuntu_snapshot == "20260728T000000Z"
  and .docker_engine_version == "29.6.2"
  and .consul.version == "1.17.3"
  and .nomad.version == "1.8.4"
  and .cni_plugins.version == "v1.6.2"
  and .clickhouse_client.version == "25.4.5.24"
  and .docker_credential_gcr.version == "2.1.32"
  and .bash_commons.revision == "013a0b429d0bd57ce49f487fade15cf95cef5b6d"
  and (
    [
      .consul.sha256,
      .nomad.sha256,
      .cni_plugins.sha256,
      .docker_credential_gcr.sha256,
      .docker_ce.sha256,
      .docker_cli.sha256,
      .containerd.sha256,
      .docker_buildx.sha256,
      .docker_compose.sha256,
      .docker_rootless.sha256,
      .docker_model.sha256,
      .gcsfuse.sha256,
      .google_cloud_ops_agent.sha256,
      .bash_commons.sha256
    ]
    | all(test("^[0-9a-f]{64}$"))
  )
  and (.clickhouse_client.sha512 | test("^[0-9a-f]{128}$"))
  and (
    [
      .docker_ce.url,
      .docker_cli.url,
      .containerd.url,
      .docker_buildx.url,
      .docker_compose.url,
      .docker_rootless.url,
      .docker_model.url,
      .gcsfuse.url,
      .google_cloud_ops_agent.url,
      .bash_commons.url
    ]
    | all(test("^https://"))
  )
' "${artifact_lock}" >/dev/null; then
  printf 'Root artifact lock is incomplete or differs from the reviewed canary inputs.\n' >&2
  exit 1
fi

grep -F 'default = "ubuntu-2404-noble-amd64-v20260723"' \
  "${variables}" >/dev/null || {
  printf 'Packer source image is not the reviewed exact Ubuntu image.\n' >&2
  exit 1
}

for variable_name in \
  build_manifest_path \
  gcp_project_id \
  gcp_zone \
  image_environment \
  image_family \
  image_name \
  network_name \
  source_revision \
  subnet_name; do
  [[ "$(grep -Ec "^variable \"${variable_name}\" \\{" "${variables}")" -eq 1 ]] || {
    printf 'Missing or duplicate required Packer variable: %s\n' \
      "${variable_name}" >&2
    exit 1
  }
done

printf 'Packer template matches the deterministic, service-account-free canary contract.\n'
