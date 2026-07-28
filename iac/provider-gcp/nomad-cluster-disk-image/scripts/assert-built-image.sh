#!/usr/bin/env bash
set -euo pipefail

gcloud_bin="${1:?usage: assert-built-image.sh GCLOUD PROJECT IMAGE FAMILY ENV REVISION}"
project_id="${2:?usage: assert-built-image.sh GCLOUD PROJECT IMAGE FAMILY ENV REVISION}"
image_name="${3:?usage: assert-built-image.sh GCLOUD PROJECT IMAGE FAMILY ENV REVISION}"
image_family="${4:?usage: assert-built-image.sh GCLOUD PROJECT IMAGE FAMILY ENV REVISION}"
environment="${5:?usage: assert-built-image.sh GCLOUD PROJECT IMAGE FAMILY ENV REVISION}"
revision="${6:?usage: assert-built-image.sh GCLOUD PROJECT IMAGE FAMILY ENV REVISION}"

image_json="$(
  "${gcloud_bin}" compute images describe "${image_name}" \
    --project="${project_id}" \
    --format=json
)"

jq -e \
  --arg project "${project_id}" \
  --arg image_name "${image_name}" \
  --arg image_family "${image_family}" \
  --arg environment "${environment}" \
  --arg revision "${revision}" '
  .name == $image_name
  and .family == $image_family
  and .status == "READY"
  and .deprecated == null
  and .labels.monad_environment == $environment
  and .labels.monad_revision == $revision
  and (.selfLink | contains("/projects/" + $project + "/global/images/" + $image_name))
' <<<"${image_json}" >/dev/null || {
  printf 'Created image does not match the reviewed one-shot candidate identity.\n' >&2
  exit 1
}

printf 'Created candidate image identity is exact and ready.\n'
