#!/usr/bin/env bash
set -euo pipefail

env_file="${1:?usage: read-env-value.sh ENV_FILE KEY}"
key="${2:?usage: read-env-value.sh ENV_FILE KEY}"

[[ "${key}" =~ ^[A-Z][A-Z0-9_]*$ ]] || {
  printf 'Invalid allowlisted environment key: %s\n' "${key}" >&2
  exit 1
}

[[ -f "${env_file}" && ! -L "${env_file}" ]] || {
  exit 0
}

matches="$(
  awk -v key="${key}" '
    $0 ~ "^[[:space:]]*" key "[[:space:]]*=" {
      value = $0
      sub("^[[:space:]]*" key "[[:space:]]*=[[:space:]]*", "", value)
      sub("\r$", "", value)
      print value
    }
  ' "${env_file}"
)"

match_count="$(
  awk -v key="${key}" '
    $0 ~ "^[[:space:]]*" key "[[:space:]]*=" { count++ }
    END { print count + 0 }
  ' "${env_file}"
)"
if [[ "${match_count}" -gt 1 ]]; then
  printf 'Duplicate allowlisted key in %s: %s\n' "${env_file}" "${key}" >&2
  exit 1
fi

value="$(awk 'NF { print; exit }' <<<"${matches}")"
if [[ "${value}" =~ ^\"[^\"]*\"$ || "${value}" =~ ^\'[^\']*\'$ ]]; then
  value="${value:1:${#value}-2}"
fi

if [[ -n "${value}" && ! "${value}" =~ ^[A-Za-z0-9][A-Za-z0-9._:/-]*$ ]]; then
  printf 'Unsafe value for allowlisted key %s in %s.\n' "${key}" "${env_file}" >&2
  exit 1
fi

printf '%s' "${value}"
