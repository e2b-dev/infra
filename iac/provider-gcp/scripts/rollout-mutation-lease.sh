#!/usr/bin/env bash
set -euo pipefail

mode="${1:-}"

usage() {
  cat >&2 <<'EOF'
usage:
  rollout-mutation-lease.sh acquire GCLOUD_BIN STATE_BUCKET PROJECT REGION HOLDER TOKEN_FILE
  rollout-mutation-lease.sh assert-held GCLOUD_BIN STATE_BUCKET PROJECT REGION TOKEN_FILE
  rollout-mutation-lease.sh transfer GCLOUD_BIN OLD_TOKEN_FILE NEW_HOLDER NEW_TOKEN_FILE
  rollout-mutation-lease.sh release GCLOUD_BIN TOKEN_FILE
  rollout-mutation-lease.sh inspect GCLOUD_BIN STATE_BUCKET PROJECT REGION

The lease object is:
  gs://STATE_BUCKET/operator-locks/PROJECT/REGION/workload-mutation.json

Acquire uses object generation-match=0. Assert-held proves that the canonical
live object still has the token's exact generation and holder. Release repeats
that proof and uses the exact acquired generation. Transfer replaces the live
object only under the old generation, captures the replacement generation, and
publishes a new private token without creating an unlocked interval. A stale
lease is never stolen or deleted automatically.
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

load_lease_token() {
  local token_file="$1"
  local token_mode

  [[ -f "${token_file}" && ! -L "${token_file}" ]] || {
    printf 'Lease token must be a regular, non-symlink file: %s\n' \
      "${token_file}" >&2
    return 1
  }
  token_mode="$(
    stat -c '%a' "${token_file}" 2>/dev/null \
      || stat -f '%Lp' "${token_file}"
  )"
  if (( (8#${token_mode} & 077) != 0 )); then
    printf 'Lease token must be private (mode 0600 or stricter): %s\n' \
      "${token_file}" >&2
    return 1
  fi
  jq -e '
    .schema_version == 1
    and (.uri | type) == "string"
    and (.bucket | type) == "string"
    and (.project | type) == "string"
    and (.region | type) == "string"
    and (.holder | type) == "string"
    and (.holder | test("^[A-Za-z0-9][A-Za-z0-9._:/-]{0,255}$"))
    and ((.generation | tostring) | test("^[0-9]+$"))
  ' "${token_file}" >/dev/null || {
    printf 'Lease token schema is invalid.\n' >&2
    return 1
  }

  lease_token_uri="$(jq -er '.uri' "${token_file}")"
  lease_token_bucket="$(jq -er '.bucket' "${token_file}")"
  lease_token_project="$(jq -er '.project' "${token_file}")"
  lease_token_region="$(jq -er '.region' "${token_file}")"
  lease_token_generation="$(jq -er '.generation | tostring' "${token_file}")"
  lease_token_holder="$(jq -er '.holder' "${token_file}")"
  require_component "${lease_token_bucket}" "state bucket"
  require_component "${lease_token_project}" "project"
  require_component "${lease_token_region}" "region"
  [[ "${lease_token_uri}" == "$(lease_uri \
    "${lease_token_bucket}" "${lease_token_project}" "${lease_token_region}")" ]] || {
    printf 'Lease token URI does not match its exact project/region scope.\n' >&2
    return 1
  }
}

assert_live_lease() {
  local gcloud_bin="$1"
  local object_json

  if ! object_json="$(
    "${gcloud_bin}" storage objects describe "${lease_token_uri}" \
      --project="${lease_token_project}" \
      --format=json
  )"; then
    printf 'Could not prove the current rollout lease object: %s\n' \
      "${lease_token_uri}" >&2
    return 1
  fi
  jq -e \
    --arg generation "${lease_token_generation}" \
    --arg holder "${lease_token_holder}" '
      (.generation | tostring) == $generation
      and (
        ([
          .metadata["monad-holder"]?,
          .custom_fields["monad-holder"]?
        ] | map(select(. != null)) | unique) == [$holder]
      )
    ' <<<"${object_json}" >/dev/null || {
    printf 'Lease token no longer matches the current object generation/holder.\n' >&2
    return 1
  }
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
    [[ "${holder}" =~ ^[A-Za-z0-9][A-Za-z0-9._:/-]{0,255}$ ]] || {
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
  assert-held)
    [[ "$#" -eq 6 ]] || usage
    gcloud_bin="$2"
    expected_bucket="$3"
    expected_project="$4"
    expected_region="$5"
    token_file="$6"
    require_component "${expected_bucket}" "state bucket"
    require_component "${expected_project}" "project"
    require_component "${expected_region}" "region"
    load_lease_token "${token_file}"
    if [[ "${lease_token_bucket}" != "${expected_bucket}" \
      || "${lease_token_project}" != "${expected_project}" \
      || "${lease_token_region}" != "${expected_region}" \
      || "${lease_token_uri}" != "$(lease_uri \
        "${expected_bucket}" "${expected_project}" "${expected_region}")" ]]; then
      printf 'Lease token does not belong to the expected canonical rollout scope.\n' >&2
      exit 1
    fi
    assert_live_lease "${gcloud_bin}"
    printf 'Confirmed shared rollout mutation lease %s generation %s held by %s.\n' \
      "${lease_token_uri}" "${lease_token_generation}" "${lease_token_holder}"
    ;;
  transfer)
    [[ "$#" -eq 5 ]] || usage
    gcloud_bin="$2"
    old_token_file="$3"
    new_holder="$4"
    new_token_file="$5"
    [[ "${new_holder}" =~ ^[A-Za-z0-9][A-Za-z0-9._:/-]{0,255}$ ]] || {
      printf 'Invalid replacement rollout-lease holder: %s\n' "${new_holder}" >&2
      exit 1
    }
    if [[ -e "${new_token_file}" || -L "${new_token_file}" ]]; then
      printf 'Replacement lease token path already exists: %s\n' \
        "${new_token_file}" >&2
      exit 1
    fi
    load_lease_token "${old_token_file}"
    assert_live_lease "${gcloud_bin}"

    temp_dir="$(mktemp -d "${TMPDIR:-/tmp}/monad-rollout-lease-transfer.XXXXXX")"
    replacement_record="${temp_dir}/lease.json"
    transferred=false
    cleanup_transfer() {
      local status=$?
      rm -rf -- "${temp_dir}"
      if [[ "${transferred}" == "true" && ! -f "${new_token_file}" ]]; then
        printf 'Lease transfer succeeded but replacement generation capture did not. The lease remains at %s under holder %s.\n' \
          "${lease_token_uri}" "${new_holder}" >&2
        printf 'Inspect and recover it manually; the old token is intentionally retained but no longer matches.\n' >&2
      fi
      exit "${status}"
    }
    trap cleanup_transfer EXIT HUP INT TERM
    umask 077
    jq -cn \
      --arg project "${lease_token_project}" \
      --arg region "${lease_token_region}" \
      --arg holder "${new_holder}" \
      --arg transferred_from "${lease_token_holder}" \
      --arg transferred_at "$(date -u +'%Y-%m-%dT%H:%M:%SZ')" '
        {
          schema_version: 1,
          project: $project,
          region: $region,
          holder: $holder,
          transferred_from: $transferred_from,
          transferred_at: $transferred_at
        }
      ' >"${replacement_record}"

    if ! "${gcloud_bin}" storage cp \
      "${replacement_record}" "${lease_token_uri}" \
      --project="${lease_token_project}" \
      --if-generation-match="${lease_token_generation}" \
      --custom-metadata="monad-holder=${new_holder}" \
      --quiet; then
      printf 'Lease transfer was not confirmed. The canonical object may still have changed; the old token is retained but must not be trusted until the live generation and holder are inspected.\n' >&2
      exit 1
    fi
    transferred=true

    object_json="$(
      "${gcloud_bin}" storage objects describe "${lease_token_uri}" \
        --project="${lease_token_project}" \
        --format=json
    )"
    replacement_generation="$(
      jq -er \
        --arg old_generation "${lease_token_generation}" \
        --arg holder "${new_holder}" '
          select(
            (.generation | tostring) != $old_generation
            and ([
              .metadata["monad-holder"]?,
              .custom_fields["monad-holder"]?
            ] | map(select(. != null)) | unique) == [$holder]
          )
          | .generation | tostring
        ' <<<"${object_json}"
    )"

    jq -cn \
      --arg uri "${lease_token_uri}" \
      --arg bucket "${lease_token_bucket}" \
      --arg project "${lease_token_project}" \
      --arg region "${lease_token_region}" \
      --arg holder "${new_holder}" \
      --arg generation "${replacement_generation}" '
        {
          schema_version: 1,
          uri: $uri,
          bucket: $bucket,
          project: $project,
          region: $region,
          holder: $holder,
          generation: $generation
        }
      ' >"${new_token_file}"
    chmod 0600 "${new_token_file}"
    rm -f -- "${old_token_file}"
    trap - EXIT HUP INT TERM
    rm -rf -- "${temp_dir}"
    printf 'Transferred shared rollout mutation lease %s from generation %s to %s under holder %s.\n' \
      "${lease_token_uri}" "${lease_token_generation}" \
      "${replacement_generation}" "${new_holder}"
    ;;
  release)
    [[ "$#" -eq 3 ]] || usage
    gcloud_bin="$2"
    token_file="$3"
    load_lease_token "${token_file}"
    assert_live_lease "${gcloud_bin}"
    "${gcloud_bin}" storage rm "${lease_token_uri}" \
      --project="${lease_token_project}" \
      --if-generation-match="${lease_token_generation}" \
      --quiet
    rm -f -- "${token_file}"
    printf 'Released shared rollout mutation lease held by %s.\n' \
      "${lease_token_holder}"
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
