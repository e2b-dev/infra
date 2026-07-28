#!/usr/bin/env bash
set -euo pipefail

gcloud_bin="${1:?usage: promote-built-image.sh GCLOUD PROJECT IMAGE CANDIDATE_FAMILY CANONICAL_FAMILY ENV REVISION}"
project_id="${2:?usage: promote-built-image.sh GCLOUD PROJECT IMAGE CANDIDATE_FAMILY CANONICAL_FAMILY ENV REVISION}"
image_name="${3:?usage: promote-built-image.sh GCLOUD PROJECT IMAGE CANDIDATE_FAMILY CANONICAL_FAMILY ENV REVISION}"
candidate_family="${4:?usage: promote-built-image.sh GCLOUD PROJECT IMAGE CANDIDATE_FAMILY CANONICAL_FAMILY ENV REVISION}"
canonical_family="${5:?usage: promote-built-image.sh GCLOUD PROJECT IMAGE CANDIDATE_FAMILY CANONICAL_FAMILY ENV REVISION}"
environment="${6:?usage: promote-built-image.sh GCLOUD PROJECT IMAGE CANDIDATE_FAMILY CANONICAL_FAMILY ENV REVISION}"
revision="${7:?usage: promote-built-image.sh GCLOUD PROJECT IMAGE CANDIDATE_FAMILY CANONICAL_FAMILY ENV REVISION}"

describe_exact() {
  "${gcloud_bin}" compute images describe "${image_name}" \
    --project="${project_id}" \
    --format=json
}

before_json="$(describe_exact)"
if ! jq -e \
  --arg project "${project_id}" \
  --arg image_name "${image_name}" \
  --arg candidate_family "${candidate_family}" \
  --arg canonical_family "${canonical_family}" \
  --arg environment "${environment}" \
  --arg revision "${revision}" '
  .name == $image_name
  and (.family == $candidate_family or .family == $canonical_family)
  and .status == "READY"
  and .deprecated == null
  and .id != null
  and (.selfLink | type) == "string"
  and .labels.monad_environment == $environment
  and .labels.monad_revision == $revision
  and (.selfLink | contains("/projects/" + $project + "/global/images/" + $image_name))
' <<<"${before_json}" >/dev/null; then
  printf 'Refusing promotion: candidate image is not the exact verified identity.\n' >&2
  exit 1
fi

before_family="$(jq -er '.family' <<<"${before_json}")"
before_id="$(jq -er '.id | tostring' <<<"${before_json}")"
before_self_link="$(jq -er '.selfLink' <<<"${before_json}")"

update_status=0
if [[ "${before_family}" == "${candidate_family}" ]]; then
  set +e
  "${gcloud_bin}" compute images update "${image_name}" \
    --project="${project_id}" \
    --family="${canonical_family}" \
    --quiet
  update_status=$?
  set -e
fi

after_json="$(describe_exact)"
family_json="$(
  "${gcloud_bin}" compute images describe-from-family "${canonical_family}" \
    --project="${project_id}" \
    --format=json
)"

if ! jq -e \
  --arg image_name "${image_name}" \
  --arg canonical_family "${canonical_family}" \
  --arg environment "${environment}" \
  --arg revision "${revision}" \
  --arg before_id "${before_id}" \
  --arg before_self_link "${before_self_link}" \
  --argjson after "${after_json}" \
  --argjson family "${family_json}" '
  $after.name == $image_name
  and $after.family == $canonical_family
  and $after.status == "READY"
  and $after.deprecated == null
  and ($after.id | tostring) == $before_id
  and $after.selfLink == $before_self_link
  and $after.labels.monad_environment == $environment
  and $after.labels.monad_revision == $revision
  and $family.name == $image_name
  and ($family.id | tostring) == $before_id
  and $family.selfLink == $before_self_link
  and $family.family == $canonical_family
  and $family.status == "READY"
' <<<"null" >/dev/null; then
  printf 'Canonical image-family promotion did not resolve to the exact verified candidate.\n' >&2
  printf 'The shared rollout lease must remain held for manual recovery.\n' >&2
  exit 1
fi

if [[ "${before_family}" == "${canonical_family}" ]]; then
  printf 'Canonical promotion was already complete and verified for exact image %s (%s).\n' \
    "${image_name}" "${before_id}"
elif [[ "${update_status}" -ne 0 ]]; then
  printf 'Recovered an ambiguous image-family update; canonical state verifies exact image %s (%s).\n' \
    "${image_name}" "${before_id}"
fi
printf 'Canonical family %s now resolves exactly to verified image %s (%s).\n' \
  "${canonical_family}" "${image_name}" "${before_id}"
