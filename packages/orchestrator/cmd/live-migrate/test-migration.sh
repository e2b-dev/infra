#!/usr/bin/env bash
# Live migration multi-node test.
# Starts two orchestrators + Redis on localhost, migrates a sandbox, verifies.
#
# Usage: sudo ./test-migration.sh --storage <PATH|gs://BUCKET> --build-id <ID> [--start-redis]
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../../../.." && pwd)"

STORAGE="" BUILD_ID="" START_REDIS=false REDIS_URL=""
NODE_A_GRPC=5008 NODE_B_GRPC=6008
CLEANUP_PIDS=() CLEANUP_DIRS=() STARTED_REDIS=false

log()  { echo -e "\033[0;36m>>>\033[0m $*"; }
ok()   { echo -e "\033[0;32m[OK]\033[0m   $*"; }
fail() { echo -e "\033[0;31m[FAIL]\033[0m $*"; exit 1; }

cleanup() {
    echo -e "\n\033[1m=== Cleanup ===\033[0m"
    for pid in "${CLEANUP_PIDS[@]}"; do
        kill -9 "$pid" 2>/dev/null && wait "$pid" 2>/dev/null || true
    done
    $STARTED_REDIS && docker rm -f e2b-migration-test-redis >/dev/null 2>&1 || true
    for dir in "${CLEANUP_DIRS[@]}"; do rm -rf "$dir"; done
}
trap cleanup EXIT

while [[ $# -gt 0 ]]; do
    case "$1" in
        --storage)      STORAGE="$2"; shift 2 ;;
        --build-id)     BUILD_ID="$2"; shift 2 ;;
        --start-redis)  START_REDIS=true; shift ;;
        --redis-url)    REDIS_URL="$2"; shift 2 ;;
        -h|--help)
            echo "Usage: sudo $0 --storage <PATH|gs://BUCKET> --build-id <ID> [--start-redis]"
            exit 0 ;;
        *) fail "Unknown: $1" ;;
    esac
done

[[ -z "$STORAGE" ]]  && fail "--storage required"
[[ -z "$BUILD_ID" ]] && fail "--build-id required"
[[ $EUID -ne 0 ]]   && fail "Must run as root"

# Resolve relative path before cd
[[ "$STORAGE" != gs://* && ! "$STORAGE" =~ ^/ ]] && STORAGE="$(realpath "$STORAGE")"

NODE_A_PROXY=$((NODE_A_GRPC - 1))
NODE_B_PROXY=$((NODE_B_GRPC - 1))

# --- Prerequisites ---
echo -e "\033[1m=== Prerequisites ===\033[0m"

lsmod | grep -q '^nbd ' || modprobe nbd nbds_max=4096
ok "nbd module"

# Find Go
GO_BIN="$(command -v go 2>/dev/null || true)"
if [[ -z "$GO_BIN" ]]; then
    for g in /home/*/.local/share/mise/installs/go/*/bin/go /usr/local/go/bin/go; do
        [[ -x "$g" ]] && GO_BIN="$g"
    done
fi
[[ -z "$GO_BIN" ]] && fail "go not found"
ok "Go: $GO_BIN"

cd "$REPO_ROOT"
"$GO_BIN" build -o /tmp/e2b-orch-test ./packages/orchestrator/ 2>&1 | tail -3
"$GO_BIN" build -o /tmp/e2b-live-migrate ./packages/orchestrator/cmd/live-migrate/ 2>&1 | tail -3
ok "Binaries built"

# Redis
if [[ -n "$REDIS_URL" ]]; then
    log "Redis: $REDIS_URL"
elif $START_REDIS; then
    docker rm -f e2b-migration-test-redis 2>/dev/null || true
    docker run -d --name e2b-migration-test-redis -p 6379:6379 redis:7-alpine >/dev/null
    STARTED_REDIS=true REDIS_URL="localhost:6379"
    sleep 1
    ok "Redis started"
else
    REDIS_URL="localhost:6379"
    redis-cli -h localhost -p 6379 ping >/dev/null 2>&1 || { REDIS_URL=""; log "Redis not available, continuing without"; }
fi

# Storage paths
if [[ "$STORAGE" == gs://* ]]; then
    SHARED="/tmp/e2b-migration-shared"; mkdir -p "$SHARED"/{kernels,fc-versions,envd,sandbox}
    CLEANUP_DIRS+=("$SHARED")
    STORAGE_PROVIDER="GCPBucket" TEMPLATE_BUCKET="${STORAGE#gs://}" LOCAL_TEMPLATE_PATH=""
    KERNELS="$SHARED/kernels" FC_VERS="$SHARED/fc-versions" ENVD="$SHARED/envd/envd" SANDBOX_DIR="$SHARED/sandbox"
else
    [[ -d "$STORAGE" ]] || fail "Storage not found: $STORAGE"
    STORAGE_PROVIDER="Local" TEMPLATE_BUCKET="" LOCAL_TEMPLATE_PATH="$(realpath "$STORAGE/templates" 2>/dev/null || realpath "$STORAGE")"
    KERNELS="$(realpath "$STORAGE/kernels" 2>/dev/null || echo "$STORAGE/kernels")"
    FC_VERS="$(realpath "$STORAGE/fc-versions" 2>/dev/null || echo "$STORAGE/fc-versions")"
    ENVD="$(realpath "$STORAGE/envd/envd" 2>/dev/null || echo "$STORAGE/envd/envd")"
    SANDBOX_DIR="$(realpath "$STORAGE/sandbox" 2>/dev/null || echo "$STORAGE/sandbox")"
fi
mkdir -p "$SANDBOX_DIR"

# --- Start Nodes ---
echo -e "\n\033[1m=== Starting Nodes ===\033[0m"

start_node() {
    local name=$1 dir=$2 grpc=$3 proxy=$4 logf=$5 offset=$6
    rm -rf "$dir"; mkdir -p "$dir"/{orchestrator/{build,build-templates,snapshot-cache,template},snapshot-cache}
    CLEANUP_DIRS+=("$dir")

    local env_file="/tmp/e2b-node-${name}.env"
    cat > "$env_file" <<EOF
ENVIRONMENT=local
NODE_ID=node-${name}
NODE_IP=127.0.0.1
GRPC_PORT=${grpc}
PROXY_PORT=${proxy}
ALLOW_SANDBOX_INTERNET=true
USE_LOCAL_NAMESPACE_STORAGE=true
ORCHESTRATOR_BASE_PATH=${dir}/orchestrator
SANDBOX_DIR=${SANDBOX_DIR}
SNAPSHOT_CACHE_DIR=${dir}/snapshot-cache
DEFAULT_CACHE_DIR=${dir}/orchestrator/build
HOST_KERNELS_DIR=${KERNELS}
FIRECRACKER_VERSIONS_DIR=${FC_VERS}
HOST_ENVD_PATH=${ENVD}
STORAGE_PROVIDER=${STORAGE_PROVIDER}
ORCHESTRATOR_LOCK_PATH=${dir}/orchestrator.lock
FORCE_STOP=true
SANDBOX_HYPERLOOP_PROXY_PORT=$((5010 + offset))
SANDBOX_NFS_PROXY_PORT=$((5011 + offset))
SANDBOX_PORTMAPPER_PORT=$((5012 + offset))
SANDBOX_TCP_FIREWALL_HTTP_PORT=$((5016 + offset))
SANDBOX_TCP_FIREWALL_TLS_PORT=$((5017 + offset))
SANDBOX_TCP_FIREWALL_OTHER_PORT=$((5018 + offset))
EOF
    [[ -n "$TEMPLATE_BUCKET" ]]    && echo "TEMPLATE_BUCKET_NAME=$TEMPLATE_BUCKET" >> "$env_file"
    [[ -n "$LOCAL_TEMPLATE_PATH" ]] && echo "LOCAL_TEMPLATE_STORAGE_BASE_PATH=$LOCAL_TEMPLATE_PATH" >> "$env_file"
    [[ -n "$REDIS_URL" ]]          && echo "REDIS_URL=${REDIS_URL#redis://}" >> "$env_file"

    (set -a; source "$env_file"; exec /tmp/e2b-orch-test) > "$logf" 2>&1 &
    CLEANUP_PIDS+=($!)

    local retries=30
    while ! ss -tlnp 2>/dev/null | grep -q ":${grpc} " && [[ $retries -gt 0 ]]; do
        kill -0 ${CLEANUP_PIDS[-1]} 2>/dev/null || { tail -20 "$logf"; fail "Node $name crashed"; }
        sleep 1; retries=$((retries - 1))
    done
    [[ $retries -eq 0 ]] && { tail -20 "$logf"; fail "Node $name timeout"; }
    ok "Node $name on :${grpc} (PID ${CLEANUP_PIDS[-1]})"
}

start_node A "/tmp/e2b-migration-node-A" $NODE_A_GRPC $NODE_A_PROXY "/tmp/e2b-node-A.log" 0
start_node B "/tmp/e2b-migration-node-B" $NODE_B_GRPC $NODE_B_PROXY "/tmp/e2b-node-B.log" 1000

# --- Run Test ---
echo -e "\n\033[1m=== Migration Test ===\033[0m"
/tmp/e2b-live-migrate \
    -test \
    -build-id "$BUILD_ID" \
    -source "127.0.0.1:${NODE_A_GRPC}" \
    -dest "127.0.0.1:${NODE_B_GRPC}" \
    -timeout 3m

exit $?
