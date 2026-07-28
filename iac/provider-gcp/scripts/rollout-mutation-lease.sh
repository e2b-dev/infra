#!/usr/bin/env bash
set -euo pipefail

mode="${1:-}"

usage() {
  cat >&2 <<'EOF'
usage:
  rollout-mutation-lease.sh acquire GCLOUD_BIN STATE_BUCKET PROJECT REGION HOLDER TOKEN_FILE
  rollout-mutation-lease.sh release GCLOUD_BIN TOKEN_FILE
  rollout-mutation-lease.sh inspect GCLOUD_BIN STATE_BUCKET PROJECT REGION

The lease object is:
  gs://STATE_BUCKET/operator-locks/PROJECT/REGION/workload-mutation.json

Acquire uses object generation-match=0. Release uses the exact acquired
generation. A stale lease is never stolen or deleted automatically.
EOF
  exit 2
}

require_component() {
  local value="$1"
  local label="$2"
  [[ "${value}" =~ ^[a-z][a-z0-9.-]{0,62}$ ]] || {
    printf 'Invalid %s for rollout lease: %s\n' "${label}" "${value}" >&2
    exit 1
  }
}

lease_uri() {
  local bucket="$1"
  local project="$2"
  local region="$3"
  printf 'gs://%s/operator-locks/%s/%s/workload-mutation.json' \
    "${bucket}" "${project}" "${region}"
}

case "${mode}" in
  acquire)
    [[ "$#" -eq 7 ]] || usage
    gcloud_bin="$2"
    bucket="$3"
    project="$4"
    region="$5"
    holder="$6"
    token_file="$7"

    require_component "${bucket}" "state bucket"
    require_component "${project}" "project"
    require_component "${region}" "region"
    [[ "${holder}" =~ ^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$ ]] || {
      printf 'Invalid rollout-lease holder: %s\n' "${holder}" >&2
      exit 1
    }
    if [[ -e "${token_file}" || -L "${token_file}" ]]; then
      printf 'Lease token path already exists: %s\n' "${token_file}" >&2
      exit 1
    fi

    uri="$(lease_uri "${bucket}" "${project}" "${region}")"
    temp_dir="$(mktemp -d "${TMPDIR:-/tmp}/monad-rollout-lease.XXXXXX")"
    lease_record="${temp_dir}/lease.json"
    uploaded=false
    cleanup_acquire() {
      local status=$?
      rm -rf -- "${temp_dir}"
      if [[ "${uploaded}" == "true" && ! -f "${token_file}" ]]; then
        printf 'Lease upload succeeded but generation capture did not. The lease remains at %s.\n' \
          "${uri}" >&2
        printf 'Inspect and recover it manually; it will not be stolen automatically (holder %s).\n' \
          "${holder}" >&2
      fi
      exit "${status}"
    }
    trap cleanup_acquire EXIT HUP INT TERM
    umask 077
    jq -cn \
      --arg project "${project}" \
      --arg region "${region}" \
      --arg holder "${holder}" \
      --arg created_at "$(date -u +'%Y-%m-%dT%H:%M:%SZ')" \
      '{
        schema_version: 1,
        project: $project,
        region: $region,
        holder: $holder,
        created_at: $created_at
      }' >"${lease_record}"

    if ! "${gcloud_bin}" storage cp \
      "${lease_record}" "${uri}" \
      --project="${project}" \
      --if-generation-match=0 \
      --custom-metadata="monad-holder=${holder}" \
      --quiet; then
      printf 'Rollout mutation lease is already held or could not be acquired: %s\n' \
        "${uri}" >&2
      printf 'Inspect it explicitly; this workflow never steals stale leases.\n' >&2
      exit 1
    fi
    uploaded=true

    object_json="$(
      "${gcloud_bin}" storage objects describe "${uri}" \
        --project="${project}" \
        --format=json
    )"
    generation="$(
      jq -er \
        --arg holder "${holder}" \
        'select(
          ([
            .metadata["monad-holder"]?,
            .custom_fields["monad-holder"]?
          ] | map(select(. != null)) | unique) == [$holder]
        ) | .generation | tostring' \
        <<<"${object_json}"
    )"

    jq -cn \
      --arg uri "${uri}" \
      --arg bucket "${bucket}" \
      --arg project "${project}" \
      --arg region "${region}" \
      --arg holder "${holder}" \
      --arg generation "${generation}" \
      '{
        schema_version: 1,
        uri: $uri,
        bucket: $bucket,
        project: $project,
        region: $region,
        holder: $holder,
        generation: $generation
      }' >"${token_file}"
    chmod 0600 "${token_file}"
    trap - EXIT HUP INT TERM
    rm -rf -- "${temp_dir}"
    printf 'Acquired shared rollout mutation lease %s generation %s.\n' \
      "${uri}" "${generation}"
    ;;
  release)
    [[ "$#" -eq 3 ]] || usage
    gcloud_bin="$2"
    token_file="$3"
    [[ -f "${token_file}" && ! -L "${token_file}" ]] || {
      printf 'Lease token must be a regular, non-symlink file: %s\n' \
        "${token_file}" >&2
      exit 1
    }
    token_mode="$(
      stat -c '%a' "${token_file}" 2>/dev/null \
        || stat -f '%Lp' "${token_file}"
    )"
    if (( (8#${token_mode} & 077) != 0 )); then
      printf 'Lease token must be private (mode 0600 or stricter): %s\n' \
        "${token_file}" >&2
      exit 1
    fi
    jq -e '
      .schema_version == 1
      and (.uri | type) == "string"
      and (.bucket | type) == "string"
      and (.project | type) == "string"
      and (.region | type) == "string"
      and (.holder | type) == "string"
      and ((.generation | tostring) | test("^[0-9]+$"))
    ' "${token_file}" >/dev/null || {
      printf 'Lease token schema is invalid.\n' >&2
      exit 1
    }
    uri="$(jq -er '.uri' "${token_file}")"
    bucket="$(jq -er '.bucket' "${token_file}")"
    project="$(jq -er '.project' "${token_file}")"
    region="$(jq -er '.region' "${token_file}")"
    generation="$(jq -er '.generation | tostring' "${token_file}")"
    holder="$(jq -er '.holder' "${token_file}")"
    require_component "${bucket}" "state bucket"
    require_component "${project}" "project"
    require_component "${region}" "region"
    [[ "${uri}" == "$(lease_uri "${bucket}" "${project}" "${region}")" ]] || {
      printf 'Lease token URI does not match its exact project/region scope.\n' >&2
      exit 1
    }
    [[ "${generation}" =~ ^[0-9]+$ ]] || {
      printf 'Invalid generation in lease token.\n' >&2
      exit 1
    }
    object_json="$(
      "${gcloud_bin}" storage objects describe "${uri}" \
        --project="${project}" \
        --format=json
    )"
    jq -e \
      --arg generation "${generation}" \
      --arg holder "${holder}" '
      (.generation | tostring) == $generation
      and (
        ([
          .metadata["monad-holder"]?,
          .custom_fields["monad-holder"]?
        ] | map(select(. != null)) | unique) == [$holder]
      )
    ' <<<"${object_json}" >/dev/null || {
      printf 'Lease token no longer matches the current object generation/holder.\n' >&2
      exit 1
    }
    "${gcloud_bin}" storage rm "${uri}" \
      --project="${project}" \
      --if-generation-match="${generation}" \
      --quiet
    rm -f -- "${token_file}"
    printf 'Released shared rollout mutation lease held by %s.\n' "${holder}"
    ;;
  inspect)
    [[ "$#" -eq 5 ]] || usage
    gcloud_bin="$2"
    bucket="$3"
    project="$4"
    region="$5"
    require_component "${bucket}" "state bucket"
    require_component "${project}" "project"
    require_component "${region}" "region"
    "${gcloud_bin}" storage objects describe \
      "$(lease_uri "${bucket}" "${project}" "${region}")" \
      --project="${project}" \
      --format='json(generation,metadata,custom_fields,creation_time,update_time)'
    ;;
  *)
    usage
    ;;
esac
