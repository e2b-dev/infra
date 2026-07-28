#!/usr/bin/env bash
set -euo pipefail

manifest_path="${1:?usage: assert-packer-artifact.sh MANIFEST PROJECT IMAGE_NAME IMAGE_FAMILY ENV REVISION SOURCE_IMAGE ROOT_INPUT_LOCK_SHA256}"
project_id="${2:?usage: assert-packer-artifact.sh MANIFEST PROJECT IMAGE_NAME IMAGE_FAMILY ENV REVISION SOURCE_IMAGE ROOT_INPUT_LOCK_SHA256}"
image_name="${3:?usage: assert-packer-artifact.sh MANIFEST PROJECT IMAGE_NAME IMAGE_FAMILY ENV REVISION SOURCE_IMAGE ROOT_INPUT_LOCK_SHA256}"
image_family="${4:?usage: assert-packer-artifact.sh MANIFEST PROJECT IMAGE_NAME IMAGE_FAMILY ENV REVISION SOURCE_IMAGE ROOT_INPUT_LOCK_SHA256}"
environment="${5:?usage: assert-packer-artifact.sh MANIFEST PROJECT IMAGE_NAME IMAGE_FAMILY ENV REVISION SOURCE_IMAGE ROOT_INPUT_LOCK_SHA256}"
revision="${6:?usage: assert-packer-artifact.sh MANIFEST PROJECT IMAGE_NAME IMAGE_FAMILY ENV REVISION SOURCE_IMAGE ROOT_INPUT_LOCK_SHA256}"
source_image="${7:?usage: assert-packer-artifact.sh MANIFEST PROJECT IMAGE_NAME IMAGE_FAMILY ENV REVISION SOURCE_IMAGE ROOT_INPUT_LOCK_SHA256}"
root_input_lock_sha256="${8:?usage: assert-packer-artifact.sh MANIFEST PROJECT IMAGE_NAME IMAGE_FAMILY ENV REVISION SOURCE_IMAGE ROOT_INPUT_LOCK_SHA256}"

[[ -f "${manifest_path}" && ! -L "${manifest_path}" ]] || {
  printf 'Packer build manifest must be a regular, non-symlink file.\n' >&2
  exit 1
}

jq -e \
  --arg project "${project_id}" \
  --arg image_name "${image_name}" \
  --arg image_family "${image_family}" \
  --arg environment "${environment}" \
  --arg revision "${revision}" \
  --arg source_image "${source_image}" \
  --arg root_input_lock_sha256 "${root_input_lock_sha256}" '
  .last_run_uuid != null
  and (.builds | length) == 1
  and .builds[0].name == "orch"
  and .builds[0].builder_type == "googlecompute"
  and .builds[0].artifact_id == ($project + "/" + $image_name)
  and .builds[0].custom_data.environment == $environment
  and .builds[0].custom_data.image_family == $image_family
  and .builds[0].custom_data.image_name == $image_name
  and .builds[0].custom_data.source_image == $source_image
  and .builds[0].custom_data.source_project == "ubuntu-os-cloud"
  and .builds[0].custom_data.source_revision == $revision
  and .builds[0].custom_data.root_input_lock_sha256 == $root_input_lock_sha256
' "${manifest_path}" >/dev/null || {
  printf 'Packer produced an unexpected or ambiguous build artifact.\n' >&2
  jq -c '{last_run_uuid, builds}' "${manifest_path}" >&2 || true
  exit 1
}

printf 'Packer manifest contains exactly the reviewed candidate image artifact.\n'
