#!/usr/bin/env bash
set -euo pipefail

project_id="${1:?usage: assert-workload-artifacts.sh PROJECT REGION PREFIX REVISION JOB_BINARY_BUCKET [GCLOUD_BIN]}"
region="${2:?usage: assert-workload-artifacts.sh PROJECT REGION PREFIX REVISION JOB_BINARY_BUCKET [GCLOUD_BIN]}"
prefix="${3:?usage: assert-workload-artifacts.sh PROJECT REGION PREFIX REVISION JOB_BINARY_BUCKET [GCLOUD_BIN]}"
revision="${4:?usage: assert-workload-artifacts.sh PROJECT REGION PREFIX REVISION JOB_BINARY_BUCKET [GCLOUD_BIN]}"
job_binary_bucket="${5:?usage: assert-workload-artifacts.sh PROJECT REGION PREFIX REVISION JOB_BINARY_BUCKET [GCLOUD_BIN]}"
gcloud_bin="${6:-gcloud}"

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
[[ "${job_binary_bucket}" =~ ^[a-z0-9][a-z0-9._-]{1,220}[a-z0-9]$ ]] || {
  printf 'Workload job binary bucket is invalid: %s\n' \
    "${job_binary_bucket}" >&2
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

describe_job_binary() {
  local object_name="$1"
  local object_uri="gs://${job_binary_bucket}/${object_name}"
  local object_json

  object_json="$(
    "${gcloud_bin}" storage objects describe \
      "${object_uri}" \
      --format=json
  )" || {
    printf 'Required workload job binary is absent: %s\n' \
      "${object_uri}" >&2
    exit 1
  }

  jq -ceS \
    --arg bucket "${job_binary_bucket}" \
    --arg name "${object_name}" \
    --argjson input "${object_json}" '
      if (
        $input.bucket == $bucket
        and $input.name == $name
        and (
          $input.generation
          | tostring
          | test("^[1-9][0-9]*$")
        )
        and ($input.size | type) == "number"
        and $input.size > 0
        and ($input.size | floor) == $input.size
        and ($input.md5_hash | type) == "string"
        and ($input.md5_hash | test("^[A-Za-z0-9+/]{22}==$"))
        and ($input.crc32c_hash | type) == "string"
        and ($input.crc32c_hash | test("^[A-Za-z0-9+/]{6}==$"))
      )
      then {
        bucket: $bucket,
        name: $name,
        generation: ($input.generation | tostring),
        size: $input.size,
        md5: $input.md5_hash,
        crc32c: $input.crc32c_hash
      }
      else error("invalid GCS job binary metadata")
      end
    ' <<<"${object_json}"
}

job_binaries='{}'
for binary in \
  orchestrator \
  template-manager \
  clean-nfs-cache \
  envd; do
  canonical="$(
    describe_job_binary "${binary}"
  )" || {
    printf 'Canonical workload job binary metadata is invalid: %s\n' \
      "${binary}" >&2
    exit 1
  }
  revision_object_name="${binary}.${revision}"
  revision_object="$(
    describe_job_binary "${revision_object_name}"
  )" || {
    printf 'Revision workload job binary metadata is invalid: %s\n' \
      "${revision_object_name}" >&2
    exit 1
  }

  jq -e \
    --argjson canonical "${canonical}" \
    --argjson revision_object "${revision_object}" '
      $canonical.size == $revision_object.size
      and $canonical.md5 == $revision_object.md5
      and $canonical.crc32c == $revision_object.crc32c
    ' >/dev/null <<<"null" || {
    printf 'Canonical workload job binary differs from reviewed revision: %s\n' \
      "${binary}" >&2
    exit 1
  }

  job_binaries="$(
    jq -ceS \
      --arg binary "${binary}" \
      --arg source "gcs::https://www.googleapis.com/storage/v1/${job_binary_bucket}/${revision_object_name}#$(jq -r '.generation' <<<"${revision_object}")" \
      --argjson canonical "${canonical}" \
      --argjson revision_object "${revision_object}" '
        . + {
          ($binary): {
            canonical: $canonical,
            revision: $revision_object,
            nomad_source: $source
          }
        }
      ' <<<"${job_binaries}"
  )"
done

jq -cnS \
  --arg project_id "${project_id}" \
  --arg region "${region}" \
  --arg repository "${repository}" \
  --arg revision "${revision}" \
  --arg job_binary_bucket "${job_binary_bucket}" \
  --argjson orchestrator_image "${orchestrator_image}" \
  --argjson core_images "${core_images}" \
  --argjson job_binaries "${job_binaries}" '
    {
      schema_version: 2,
      gcp_project_id: $project_id,
      gcp_region: $region,
      core_repository: $repository,
      core_image_revision: $revision,
      job_binary_bucket: $job_binary_bucket,
      orchestrator_image: $orchestrator_image,
      core_images: $core_images,
      job_binaries: $job_binaries
    }
  '
