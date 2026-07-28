#!/usr/bin/env bash

set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
provider_dir="$(cd "${script_dir}/.." && pwd)"
test_dir="$(mktemp -d)"
trap 'rm -rf "$test_dir"' EXIT

for target in plan apply destroy; do
  log="${test_dir}/${target}.log"
  if make -C "$provider_dir" "$target" >"$log" 2>&1; then
    echo "Expected legacy workload target '${target}' to refuse execution." >&2
    exit 1
  fi

  grep -F "Refusing legacy workload ${target}" "$log" >/dev/null
done

echo "Legacy workload mutation targets remain disabled."
