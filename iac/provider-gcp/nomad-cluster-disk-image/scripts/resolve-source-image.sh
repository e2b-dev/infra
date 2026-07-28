#!/usr/bin/env bash
set -euo pipefail

gcloud_bin="${1:?usage: resolve-source-image.sh GCLOUD_BIN SOURCE_PROJECT SOURCE_IMAGE}"
source_project="${2:?usage: resolve-source-image.sh GCLOUD_BIN SOURCE_PROJECT SOURCE_IMAGE}"
source_image="${3:?usage: resolve-source-image.sh GCLOUD_BIN SOURCE_PROJECT SOURCE_IMAGE}"

image_json="$(
  "${gcloud_bin}" compute images describe "${source_image}" \
    --project="${source_project}" \
    --format=json
)"

if ! jq -e '
  .name != null
  and .id != null
  and .selfLink != null
  and .archiveSizeBytes != null
  and .diskSizeGb != null
  and .status == "READY"
  and (.deprecated == null)
' <<<"${image_json}" >/dev/null; then
  printf 'Source image must be a ready, non-deprecated image with immutable identity fields.\n' >&2
  exit 1
fi

jq -cS '{
  archiveSizeBytes,
  deprecated: (.deprecated // null),
  diskSizeGb,
  id,
  name,
  selfLink,
  status
}' <<<"${image_json}"
