#!/usr/bin/env bash
set -euo pipefail

packer_bin="${1:?usage: assert-packer-plugin.sh PACKER_BIN PLUGIN_ROOT}"
plugin_root="${2:?usage: assert-packer-plugin.sh PACKER_BIN PLUGIN_ROOT}"

command -v jq >/dev/null 2>&1 || {
  printf 'jq is required to verify the Packer plugin identity.\n' >&2
  exit 1
}

if [[ ! -d "${plugin_root}" || -L "${plugin_root}" ]]; then
  printf 'Isolated Packer plugin directory is missing or unsafe: %s\n' \
    "${plugin_root}" >&2
  exit 1
fi
plugin_root="$(cd "${plugin_root}" && pwd -P)"
unsafe_symlink="$(find "${plugin_root}" -type l -print -quit)"
if [[ -n "${unsafe_symlink}" ]]; then
  printf 'Isolated Packer plugin directory must not contain symlinks: %s\n' \
    "${unsafe_symlink}" >&2
  exit 1
fi

mapfile_compat() {
  local item
  while IFS= read -r item; do
    [[ -n "${item}" ]] && printf '%s\0' "${item}"
  done
}

installed_output="$(
  PACKER_PLUGIN_PATH="${plugin_root}" \
    "${packer_bin}" plugins installed -machine-readable
)"

plugin_paths=()
while IFS= read -r -d '' plugin_path; do
  plugin_paths+=("${plugin_path}")
done < <(
  sed -n 's/^[^,]*,,ui,message,//p' <<<"${installed_output}" \
    | grep '/googlecompute/packer-plugin-googlecompute_v1\.0\.16_' \
    | mapfile_compat
)

if [[ "${#plugin_paths[@]}" -ne 1 ]]; then
  printf 'Expected exactly one isolated googlecompute v1.0.16 plugin; found %s.\n' \
    "${#plugin_paths[@]}" >&2
  exit 1
fi

reported_plugin_path="${plugin_paths[0]}"
plugin_path="$(
  cd "$(dirname "${reported_plugin_path}")" &&
    printf '%s/%s' "$(pwd -P)" "$(basename "${reported_plugin_path}")"
)"
checksum_path="${plugin_path}_SHA256SUM"
if [[ "${plugin_path}" != "${plugin_root}/"* ]]; then
  printf 'Packer reported a plugin outside the isolated directory: %s\n' \
    "${plugin_path}" >&2
  exit 1
fi

for path in "${plugin_path}" "${checksum_path}"; do
  if [[ ! -f "${path}" || -L "${path}" ]]; then
    printf 'Packer plugin input must be a regular, non-symlink file: %s\n' \
      "${path}" >&2
    exit 1
  fi
done
[[ -x "${plugin_path}" ]] || {
  printf 'Packer plugin must be executable: %s\n' "${plugin_path}" >&2
  exit 1
}

expected_sha256="$(tr -d '[:space:]' <"${checksum_path}")"
actual_sha256="$(shasum -a 256 "${plugin_path}" | awk '{print $1}')"

if [[ ! "${expected_sha256}" =~ ^[0-9a-f]{64}$ \
  || "${actual_sha256}" != "${expected_sha256}" ]]; then
  printf 'Packer googlecompute plugin checksum verification failed.\n' >&2
  exit 1
fi

jq -cn \
  --arg path "${plugin_path}" \
  --arg sha256 "${actual_sha256}" \
  --arg mode "$(
    if stat -c '%a' "${plugin_path}" >/dev/null 2>&1; then
      stat -c '%a' "${plugin_path}"
    else
      stat -f '%Lp' "${plugin_path}"
    fi
  )" \
  --arg checksum_sha256 "$(
    shasum -a 256 "${checksum_path}" | awk '{print $1}'
  )" \
  '{
    path: $path,
    mode: $mode,
    sha256: $sha256,
    checksum_sha256: $checksum_sha256
  }'
