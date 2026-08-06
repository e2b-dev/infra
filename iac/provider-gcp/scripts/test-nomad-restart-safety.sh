#!/usr/bin/env bash
set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
nomad_script="${root_dir}/nomad-cluster/scripts/run-nomad.sh"
work_dir="$(mktemp -d)"
trap 'rm -rf -- "${work_dir}"' EXIT

# shellcheck source=/dev/null
source "$nomad_script"

# Cleanup is deliberately exact: remove the two historical CLI documents and
# leave every unrelated file alone.
cleanup_fixture="${work_dir}/cleanup-config"
mkdir -p "$cleanup_fixture"
touch \
  "${cleanup_fixture}/api_node_pool.hcl" \
  "${cleanup_fixture}/build_node_pool.hcl" \
  "${cleanup_fixture}/operator-managed.hcl"

remove_legacy_node_pool_documents "$cleanup_fixture"
test ! -e "${cleanup_fixture}/api_node_pool.hcl"
test ! -e "${cleanup_fixture}/build_node_pool.hcl"
test -e "${cleanup_fixture}/operator-managed.hcl"

runtime_config_dir="${work_dir}/runtime/config"
runtime_data_dir="${work_dir}/runtime/data"
runtime_bin_dir="${work_dir}/runtime/bin"
runtime_log_dir="${work_dir}/runtime/log"
runtime_supervisor_config="${work_dir}/runtime/run-nomad.conf"
mkdir -p \
  "$runtime_config_dir" \
  "$runtime_data_dir" \
  "$runtime_bin_dir" \
  "$runtime_log_dir" \
  "${work_dir}/stub-bin" \
  "${work_dir}/node-pool-tmp"

cat >"${runtime_config_dir}/default.hcl" <<'EOF'
datacenter = "restart-safety-test"
EOF
cat >"${runtime_config_dir}/api_node_pool.hcl" <<'EOF'
node_pool "api" {}
EOF
cat >"${runtime_config_dir}/build_node_pool.hcl" <<'EOF'
node_pool "build" {}
EOF

remove_legacy_node_pool_documents "$runtime_config_dir"
runtime_config_files="$(
  find "$runtime_config_dir" -mindepth 1 -maxdepth 1 -type f -exec basename {} \; |
    LC_ALL=C sort
)"
if [[ "$runtime_config_files" != "default.hcl" ]]; then
  printf 'Nomad agent config directory contains unexpected files after legacy cleanup:\n%s\n' \
    "${runtime_config_files:-<none>}" >&2
  exit 1
fi

cat >"${work_dir}/stub-bin/nomad" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

function mode_of {
  local -r path="$1"

  if stat -c '%a' "$path" >/dev/null 2>&1; then
    stat -c '%a' "$path"
  else
    stat -f '%Lp' "$path"
  fi
}

case "${1:-}" in
  agent)
    test "${2:-}" = "-config"
    test "${3:-}" = "$NOMAD_TEST_CONFIG_DIR"
    test "${4:-}" = "-data-dir"
    test "${5:-}" = "$NOMAD_TEST_DATA_DIR"
    files="$(
      find "$NOMAD_TEST_CONFIG_DIR" -mindepth 1 -maxdepth 1 -type f -exec basename {} \; |
        LC_ALL=C sort
    )"
    test "$files" = "default.hcl"
    printf 'agent\n' >>"$NOMAD_TEST_CALLS"
    ;;
  node)
    test "${2:-}" = "pool"
    test "${3:-}" = "apply"
    document="${6:-}"
    test -f "$document"
    case "$document" in
      "$NOMAD_TEST_CONFIG_DIR"/*)
        printf 'Node-pool document was written into the agent config directory: %s\n' "$document" >&2
        exit 1
        ;;
      "$NOMAD_TEST_TMPDIR"/nomad-node-pools.*/*)
        ;;
      *)
        printf 'Node-pool document was not written to the bounded temporary directory: %s\n' "$document" >&2
        exit 1
        ;;
    esac
    test "$(mode_of "$(dirname "$document")")" = "700"
    test "$(mode_of "$document")" = "600"
    case "$(basename "$document")" in
      api_node_pool.hcl)
        grep -Fq 'node_pool "api"' "$document"
        ;;
      build_node_pool.hcl)
        grep -Fq 'node_pool "build"' "$document"
        ;;
      *)
        exit 1
        ;;
    esac
    printf 'apply:%s\n' "$(basename "$document")" >>"$NOMAD_TEST_CALLS"
    if [[ "${NOMAD_TEST_FAIL_POOL:-}" == "$(basename "$document")" ]]; then
      exit 42
    fi
    ;;
  *)
    printf 'Unexpected Nomad invocation: %s\n' "$*" >&2
    exit 1
    ;;
esac
EOF

cat >"${work_dir}/stub-bin/supervisorctl" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

case "${1:-}" in
  reread)
    printf 'supervisor:reread\n' >>"$NOMAD_TEST_CALLS"
    ;;
  update)
    grep -Fxq \
      "command=$NOMAD_TEST_BIN_DIR/nomad agent -config $NOMAD_TEST_CONFIG_DIR -data-dir $NOMAD_TEST_DATA_DIR" \
      "$NOMAD_TEST_SUPERVISOR_CONFIG"
    "$NOMAD_TEST_BIN_DIR/nomad" agent -config "$NOMAD_TEST_CONFIG_DIR" -data-dir "$NOMAD_TEST_DATA_DIR"
    printf 'supervisor:update\n' >>"$NOMAD_TEST_CALLS"
    ;;
  restart)
    test "${2:-}" = "nomad"
    "$NOMAD_TEST_BIN_DIR/nomad" agent -config "$NOMAD_TEST_CONFIG_DIR" -data-dir "$NOMAD_TEST_DATA_DIR"
    printf 'supervisor:restart\n' >>"$NOMAD_TEST_CALLS"
    ;;
  *)
    printf 'Unexpected Supervisor invocation: %s\n' "$*" >&2
    exit 1
    ;;
esac
EOF

chmod 0755 \
  "${work_dir}/stub-bin/nomad" \
  "${work_dir}/stub-bin/supervisorctl"
ln -s "${work_dir}/stub-bin/nomad" "${runtime_bin_dir}/nomad"

calls_file="${work_dir}/calls.log"
: >"$calls_file"

export NOMAD_TEST_BIN_DIR="$runtime_bin_dir"
export NOMAD_TEST_CALLS="$calls_file"
export NOMAD_TEST_CONFIG_DIR="$runtime_config_dir"
export NOMAD_TEST_DATA_DIR="$runtime_data_dir"
export NOMAD_TEST_SUPERVISOR_CONFIG="$runtime_supervisor_config"
export NOMAD_TEST_TMPDIR="${work_dir}/node-pool-tmp"

PATH="${work_dir}/stub-bin:${PATH}" \
  TMPDIR="$NOMAD_TEST_TMPDIR" \
  create_node_pools 'test-token'

expected_applies=$'apply:api_node_pool.hcl\napply:build_node_pool.hcl'
actual_applies="$(grep '^apply:' "$calls_file")"
if [[ "$actual_applies" != "$expected_applies" ]]; then
  printf 'Unexpected node-pool apply sequence:\n%s\n' "${actual_applies:-<none>}" >&2
  exit 1
fi
if find "$NOMAD_TEST_TMPDIR" -mindepth 1 -print -quit | grep -q .; then
  printf 'Nomad node-pool apply left temporary documents behind.\n' >&2
  exit 1
fi
runtime_config_files="$(
  find "$runtime_config_dir" -mindepth 1 -maxdepth 1 -type f -exec basename {} \; |
    LC_ALL=C sort
)"
test "$runtime_config_files" = "default.hcl"

: >"$calls_file"
if PATH="${work_dir}/stub-bin:${PATH}" \
  TMPDIR="$NOMAD_TEST_TMPDIR" \
  NOMAD_TEST_FAIL_POOL=build_node_pool.hcl \
  create_node_pools 'test-token'; then
  printf 'A failed node-pool apply unexpectedly succeeded.\n' >&2
  exit 1
fi
if find "$NOMAD_TEST_TMPDIR" -mindepth 1 -print -quit | grep -q .; then
  printf 'Failed Nomad node-pool apply left temporary documents behind.\n' >&2
  exit 1
fi
: >"$calls_file"

generate_supervisor_config \
  "$runtime_supervisor_config" \
  "$runtime_config_dir" \
  "$runtime_data_dir" \
  "$runtime_bin_dir" \
  "$runtime_log_dir" \
  "$(id -un)" \
  false

PATH="${work_dir}/stub-bin:${PATH}" start_nomad
PATH="${work_dir}/stub-bin:${PATH}" supervisorctl restart nomad

test "$(grep -c '^agent$' "$calls_file")" -eq 2
grep -Fqx 'supervisor:reread' "$calls_file"
grep -Fqx 'supervisor:update' "$calls_file"
grep -Fqx 'supervisor:restart' "$calls_file"

# Exercise run's orchestration order in an isolated copy of the normal
# /opt/nomad/{bin,config,data,log} layout. start_nomad fails if either legacy
# document is still present when the service would be started.
run_fixture="${work_dir}/run-fixture"
mkdir -p \
  "${run_fixture}/bin" \
  "${run_fixture}/config" \
  "${run_fixture}/data" \
  "${run_fixture}/log"
cp "$nomad_script" "${run_fixture}/bin/run-nomad.sh"
cat >"${run_fixture}/config/api_node_pool.hcl" <<'EOF'
node_pool "api" {}
EOF
cat >"${run_fixture}/config/build_node_pool.hcl" <<'EOF'
node_pool "build" {}
EOF

bash -c '
  set -eo pipefail
  source "$1"

  assert_is_installed() { :; }
  generate_nomad_config() {
    local -r target_config_dir="$4"
    printf "%s\n" "datacenter = \"restart-order-test\"" >"$target_config_dir/default.hcl"
  }
  generate_supervisor_config() { :; }
  start_nomad() {
    test ! -e "$config_dir/api_node_pool.hcl"
    test ! -e "$config_dir/build_node_pool.hcl"
    files="$(
      find "$config_dir" -mindepth 1 -maxdepth 1 -type f -exec basename {} \; |
        LC_ALL=C sort
    )"
    test "$files" = "default.hcl"
  }
  bootstrap() { :; }
  create_node_pools() { :; }

  use_sudo=""
  node_pool=""
  node_labels=""
  orchestrator_job_version=""
  run --server --num-servers 3 --consul-token test-consul --nomad-token test-nomad
' bash "${run_fixture}/bin/run-nomad.sh"

printf 'Nomad restart safety regression test passed.\n'
