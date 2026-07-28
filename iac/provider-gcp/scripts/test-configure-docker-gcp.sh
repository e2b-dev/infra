#!/usr/bin/env bash

set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
configure_script="${script_dir}/../nomad-cluster/scripts/configure-docker-gcp.sh"
test_dir="$(mktemp -d)"
trap 'rm -rf "$test_dir"' EXIT

mkdir -p "${test_dir}/bin"
helper_log="${test_dir}/helper.log"

printf '%s\n' \
  '#!/usr/bin/env bash' \
  'set -euo pipefail' \
  'printf "HOME=%s ARGS=%s\\n" "$HOME" "$*" >"$HELPER_LOG"' \
  >"${test_dir}/bin/docker-credential-gcr"
chmod 0755 "${test_dir}/bin/docker-credential-gcr"

PATH="${test_dir}/bin:${PATH}" \
  HELPER_LOG="$helper_log" \
  DOCKER_CREDENTIAL_HOME="${test_dir}/home" \
  NOMAD_DOCKER_CONFIG_DIR="${test_dir}/nomad" \
  "$configure_script" "us-east4-docker.pkg.dev"

grep -F "HOME=${test_dir}/home ARGS=config --token-source=env" "$helper_log" >/dev/null

expected='{"credHelpers":{"us-east4-docker.pkg.dev":"gcr"}}'
test "$(cat "${test_dir}/nomad/config.json")" = "$expected"
test "$(cat "${test_dir}/home/.docker/config.json")" = "$expected"

file_mode() {
  stat -c '%a' "$1" 2>/dev/null || stat -f '%Lp' "$1"
}

test "$(file_mode "${test_dir}/nomad/config.json")" = "600"
test "$(file_mode "${test_dir}/home/.docker/config.json")" = "600"

echo "Keyless Docker helper configuration test passed."
