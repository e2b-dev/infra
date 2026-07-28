#!/usr/bin/env bash
set -euo pipefail

project_id="${1:?usage: assert-workload-artifacts.sh PROJECT REGION PREFIX REVISION [GCLOUD_BIN]}"
region="${2:?usage: assert-workload-artifacts.sh PROJECT REGION PREFIX REVISION [GCLOUD_BIN]}"
prefix="${3:?usage: assert-workload-artifacts.sh PROJECT REGION PREFIX REVISION [GCLOUD_BIN]}"
revision="${4:?usage: assert-workload-artifacts.sh PROJECT REGION PREFIX REVISION [GCLOUD_BIN]}"
gcloud_bin="${5:-gcloud}"

[[ "${revision}" =~ ^[0-9a-f]{12,40}$ ]] || {
  printf 'Workload image revision must be a 12-40 character lowercase Git SHA: %s\n' \
    "${revision}" >&2
  exit 1
}
if [[ ! -x "${gcloud_bin}" ]] && ! command -v "${gcloud_bin}" >/dev/null 2>&1; then
  printf 'gcloud is not installed or executable: %s\n' "${gcloud_bin}" >&2
  exit 1
fi
command -v jq >/dev/null 2>&1 || {
  printf 'jq is required to inspect workload artifacts.\n' >&2
  exit 1
}

family_json="$(
  "${gcloud_bin}" compute images describe-from-family e2b-orch \
    --project="${project_id}" \
    --format=json
)" || {
  printf 'Required GCE image family e2b-orch is absent in project %s.\n' \
    "${project_id}" >&2
  exit 1
}
orchestrator_image="$(
  jq -ceS '
  (.name | type) == "string"
  and (.name | length) > 0
  and (.selfLink | type) == "string"
  and (.selfLink | length) > 0
  and .status == "READY"
  and (
    (.deprecated.state? // "")
    | IN("", "ACTIVE")
  )
  |
  if .
  then {
    family: "e2b-orch",
    project: $project_id,
    status: $input.status,
    name: $input.name,
    self_link: $input.selfLink,
    id: (
      if $input.id == null then null else ($input.id | tostring) end
    )
  }
  else error("no active concrete image")
  end
  ' \
    --arg project_id "${project_id}" \
    --argjson input "${family_json}" <<<"${family_json}"
)" || {
  printf 'Required GCE image family e2b-orch has no active concrete image.\n' >&2
  exit 1
}

repository="${prefix}core"
images=(
  api
  db-migrator
  client-proxy
  docker-reverse-proxy
  clickhouse-migrator
)
core_images='{}'

image_digest() {
  jq -er '
    .image_summary.digest
    // .imageSummary.digest
    // .digest
    // empty
    | select(type == "string" and test("^sha256:[0-9a-f]{64}$"))
  '
}

for image in "${images[@]}"; do
  image_root="${region}-docker.pkg.dev/${project_id}/${repository}/${image}"
  revision_json="$(
    "${gcloud_bin}" artifacts docker images describe \
      "${image_root}:${revision}" \
      --project="${project_id}" \
      --format=json
  )" || {
    printf 'Required core image is absent: %s:%s\n' "${image_root}" "${revision}" >&2
    exit 1
  }
  latest_json="$(
    "${gcloud_bin}" artifacts docker images describe \
      "${image_root}:latest" \
      --project="${project_id}" \
      --format=json
  )" || {
    printf 'Required core image is absent: %s:latest\n' "${image_root}" >&2
    exit 1
  }

  revision_digest="$(image_digest <<<"${revision_json}")" || {
    printf 'Revision image has no valid digest: %s:%s\n' "${image_root}" "${revision}" >&2
    exit 1
  }
  latest_digest="$(image_digest <<<"${latest_json}")" || {
    printf 'Latest image has no valid digest: %s:latest\n' "${image_root}" >&2
    exit 1
  }
  if [[ "${revision_digest}" != "${latest_digest}" ]]; then
    printf 'Core image latest does not match reviewed revision for %s.\n' "${image_root}" >&2
    printf 'Revision %s: %s\nLatest: %s\n' \
      "${revision}" "${revision_digest}" "${latest_digest}" >&2
    exit 1
  fi
  core_images="$(
    jq -ceS \
      --arg image "${image}" \
      --arg revision_ref "${image_root}:${revision}" \
      --arg revision_digest "${revision_digest}" \
      --arg revision_resolved_ref "${image_root}@${revision_digest}" \
      --arg latest_ref "${image_root}:latest" \
      --arg latest_digest "${latest_digest}" \
      --arg latest_resolved_ref "${image_root}@${latest_digest}" '
        . + {
          ($image): {
            revision: {
              reference: $revision_ref,
              digest: $revision_digest,
              resolved_reference: $revision_resolved_ref
            },
            latest: {
              reference: $latest_ref,
              digest: $latest_digest,
              resolved_reference: $latest_resolved_ref
            }
          }
        }
      ' <<<"${core_images}"
  )"
done

jq -cnS \
  --arg project_id "${project_id}" \
  --arg region "${region}" \
  --arg repository "${repository}" \
  --arg revision "${revision}" \
  --argjson orchestrator_image "${orchestrator_image}" \
  --argjson core_images "${core_images}" '
    {
      schema_version: 1,
      gcp_project_id: $project_id,
      gcp_region: $region,
      core_repository: $repository,
      core_image_revision: $revision,
      orchestrator_image: $orchestrator_image,
      core_images: $core_images
    }
  '
