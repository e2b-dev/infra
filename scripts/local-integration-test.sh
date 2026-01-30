#!/usr/bin/env bash
#
# Run integration tests locally, similar to CI.
# Usage: ./scripts/local-integration-test.sh [--gcs|--local] [--skip-setup] [TEST_NAME]
#
# Examples:
#   ./scripts/local-integration-test.sh                    # Run all tests with local storage
#   ./scripts/local-integration-test.sh --local            # Run all tests with local storage
#   ./scripts/local-integration-test.sh --gcs              # Run all tests with GCS storage
#   ./scripts/local-integration-test.sh TestDeleteTemplate # Run single test with local storage
#   ./scripts/local-integration-test.sh --skip-setup TestFoo # Skip setup, run single test
#
# Environment variables:
#   E2B_LOCAL_DIR - Base directory for test artifacts (default: ~/.e2b-local)
#

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

# Base directory for all local test artifacts (can be overridden with E2B_LOCAL_DIR)
E2B_LOCAL_DIR="${E2B_LOCAL_DIR:-$HOME/.e2b-local}"

# Defaults
STORAGE_MODE="local"
TEST_NAME=""
SKIP_SETUP=false

# Parse arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        --gcs)
            STORAGE_MODE="gcs"
            shift
            ;;
        --local)
            STORAGE_MODE="local"
            shift
            ;;
        --skip-setup)
            SKIP_SETUP=true
            shift
            ;;
        *)
            TEST_NAME="$1"
            shift
            ;;
    esac
done

echo "=== Local Integration Test Runner ==="
echo "Base directory: $E2B_LOCAL_DIR"
echo "Storage mode: $STORAGE_MODE"
echo "Test filter: ${TEST_NAME:-all}"
echo ""

# Check if running as root for certain operations
check_sudo() {
    if ! sudo -n true 2>/dev/null; then
        echo "Some operations require sudo. You may be prompted for your password."
    fi
}

# Setup host environment (directories, modules, etc.)
setup_host() {
    echo "=== Setting up host environment ==="

    # Create local directories (no sudo needed)
    # Note: create-build expects kernels/ and fc-versions/ under storage path
    mkdir -p "$E2B_LOCAL_DIR/orchestrator"/{sandbox,template,build}
    mkdir -p "$E2B_LOCAL_DIR/fc-vm"
    mkdir -p "$E2B_LOCAL_DIR/fc-envd"
    mkdir -p "$E2B_LOCAL_DIR/kernels"
    mkdir -p "$E2B_LOCAL_DIR/fc-versions"
    mkdir -p "$E2B_LOCAL_DIR/templates"
    mkdir -p "$E2B_LOCAL_DIR/sandbox"
    mkdir -p "$E2B_LOCAL_DIR/snapshot-cache"

    # These still need sudo for special mounts
    sudo mkdir -p /mnt/hugepages
    sudo mkdir -p /mnt/snapshot-cache

    # Load nbd module if not loaded
    if ! lsmod | grep -q nbd; then
        echo "Loading nbd module..."
        sudo modprobe nbd nbds_max=4096
    fi

    # Mount hugetlbfs if not mounted
    if ! mount | grep -q "hugetlbfs.*hugepages"; then
        echo "Mounting hugetlbfs..."
        sudo mount -t hugetlbfs none /mnt/hugepages 2>/dev/null || true
    fi

    # Setup hugepages (minimal for local testing)
    current_hugepages=$(cat /proc/sys/vm/nr_hugepages)
    if [[ "$current_hugepages" -lt 512 ]]; then
        echo "Setting up hugepages (512)..."
        echo 512 | sudo tee /proc/sys/vm/nr_hugepages > /dev/null
        echo 512 | sudo tee /proc/sys/vm/nr_overcommit_hugepages > /dev/null
    fi

    # Mount tmpfs for snapshot cache if not mounted
    if ! mount | grep -q "/mnt/snapshot-cache"; then
        echo "Mounting tmpfs for snapshot cache..."
        sudo mount -t tmpfs -o size=8G tmpfs /mnt/snapshot-cache 2>/dev/null || true
    fi

    # Increase file descriptor limit
    ulimit -n 1048576 2>/dev/null || true

    echo "Host setup complete."
}

# Download kernels and firecracker versions
download_artifacts() {
    echo "=== Downloading artifacts ==="

    # create-build expects kernels in <storage>/kernels/<version>/vmlinux.bin
    if [[ ! -f "$E2B_LOCAL_DIR/kernels/vmlinux-6.1.158/vmlinux.bin" ]]; then
        echo "Downloading kernels..."
        gsutil -m cp -r gs://e2b-prod-public-builds/kernels/* "$E2B_LOCAL_DIR/kernels/"
        chmod -R 755 "$E2B_LOCAL_DIR/kernels"
    else
        echo "Kernels already present."
    fi

    # create-build expects firecracker in <storage>/fc-versions/<version>/firecracker
    if [[ ! -f "$E2B_LOCAL_DIR/fc-versions/v1.12.1_717921c/firecracker" ]]; then
        echo "Downloading firecracker versions..."
        gsutil -m cp -r gs://e2b-prod-public-builds/firecrackers/* "$E2B_LOCAL_DIR/fc-versions/"
        chmod -R 755 "$E2B_LOCAL_DIR/fc-versions"
    else
        echo "Firecracker versions already present."
    fi

    echo "Artifacts ready."
}

# Build envd
build_envd() {
    echo "=== Building envd ==="
    cd "$ROOT_DIR"
    make build/envd
    cp packages/envd/bin/debug/envd "$E2B_LOCAL_DIR/fc-envd/"
    chmod 755 "$E2B_LOCAL_DIR/fc-envd/envd"
    echo "envd built and installed."
}

# Setup environment file
setup_env() {
    echo "=== Setting up environment ==="

    cd "$ROOT_DIR"

    # Create .env.localtest
    cat > .env.localtest << EOF
# Auto-generated for local integration testing
ENVIRONMENT=local
NODE_ID=local-test-node
NODE_IP=127.0.0.1

# Local paths (under E2B_LOCAL_DIR) - matching create-build layout
E2B_LOCAL_DIR=$E2B_LOCAL_DIR
HOST_KERNELS_DIR=$E2B_LOCAL_DIR/kernels
FIRECRACKER_VERSIONS_DIR=$E2B_LOCAL_DIR/fc-versions
FC_ENVD_DIR=$E2B_LOCAL_DIR/fc-envd
ORCHESTRATOR_BASE_PATH=$E2B_LOCAL_DIR/orchestrator
SANDBOX_DIR=$E2B_LOCAL_DIR/sandbox
SNAPSHOT_CACHE_DIR=$E2B_LOCAL_DIR/snapshot-cache

# Storage
STORAGE_PROVIDER=${STORAGE_MODE^^}
ARTIFACTS_REGISTRY_PROVIDER=${STORAGE_MODE^^}
EOF

    if [[ "$STORAGE_MODE" == "local" ]]; then
        cat >> .env.localtest << EOF
LOCAL_TEMPLATE_STORAGE_BASE_PATH=$E2B_LOCAL_DIR/templates
LOCAL_BUILD_CACHE_STORAGE_BASE_PATH=$E2B_LOCAL_DIR/build-cache
EOF
        mkdir -p "$E2B_LOCAL_DIR/templates" "$E2B_LOCAL_DIR/build-cache"
    else
        cat >> .env.localtest << EOF
TEMPLATE_BUCKET_NAME=e2b-staging-lev-templates
BUILD_CACHE_BUCKET_NAME=e2b-staging-lev-build-cache
GCP_PROJECT_ID=e2b-staging-lev
EOF
    fi

    cat >> .env.localtest << EOF

# Services
ORCHESTRATOR_SERVICES=orchestrator,template-manager
SANDBOX_ACCESS_TOKEN_HASH_SEED=localtest123456789
SHARED_CHUNK_CACHE_PATH=$E2B_LOCAL_DIR/chunk-cache
MAX_PARALLEL_MEMFILE_SNAPSHOTTING=2

# Test config
TESTS_API_SERVER_URL=http://localhost:3000
TESTS_ORCHESTRATOR_HOST=localhost:5008
TESTS_ENVD_PROXY=http://localhost:5007
TESTS_SANDBOX_TEMPLATE_ID=base
TESTS_E2B_API_KEY=e2b_localtest_key_12345
TESTS_E2B_ACCESS_TOKEN=sk_e2b_localtest_token_12345
TESTS_SANDBOX_TEAM_ID=00000000-0000-0000-0000-000000000001
TESTS_SANDBOX_USER_ID=00000000-0000-0000-0000-000000000002

# Database (using local postgres)
POSTGRES_CONNECTION_STRING=postgresql://postgres:local@localhost:5432/mydatabase?sslmode=disable
EOF

    echo "localtest" > .last_used_env
    mkdir -p "$E2B_LOCAL_DIR/chunk-cache"

    echo "Environment configured for $STORAGE_MODE storage."
}

# Start local services (postgres, redis, clickhouse)
start_dependencies() {
    echo "=== Starting dependencies ==="

    # Check if postgres is running
    if ! docker ps | grep -q postgres; then
        echo "Starting PostgreSQL..."
        docker run -d --name postgres \
            -e POSTGRES_USER=postgres \
            -e POSTGRES_PASSWORD=local \
            -e POSTGRES_DB=mydatabase \
            -p 5432:5432 \
            --health-cmd="pg_isready -U postgres" \
            --health-interval=5s \
            --health-timeout=2s \
            --health-retries=5 \
            postgres:latest

        echo "Waiting for PostgreSQL..."
        while [ "$(docker inspect -f '{{.State.Health.Status}}' postgres 2>/dev/null)" != "healthy" ]; do
            sleep 2
        done

        docker exec postgres psql -U postgres -d mydatabase -c "CREATE SCHEMA IF NOT EXISTS extensions; CREATE EXTENSION IF NOT EXISTS pgcrypto SCHEMA extensions;"

        cd "$ROOT_DIR"
        make migrate
    else
        echo "PostgreSQL already running."
    fi

    # Check if redis is running
    if ! docker ps | grep -q redis; then
        echo "Starting Redis..."
        docker run -d --name redis \
            -p 6379:6379 \
            --health-cmd="redis-cli ping" \
            --health-interval=5s \
            --health-timeout=2s \
            --health-retries=5 \
            redis:latest

        echo "Waiting for Redis..."
        while [ "$(docker inspect -f '{{.State.Health.Status}}' redis 2>/dev/null)" != "healthy" ]; do
            sleep 2
        done
    else
        echo "Redis already running."
    fi

    echo "Dependencies ready."
}

# Build a test template
build_template() {
    echo "=== Building test template ==="

    cd "$ROOT_DIR"

    TEMPLATE_ID="localtest-template"
    BUILD_ID=$(uuidgen)

    echo "Building template $TEMPLATE_ID with build $BUILD_ID..."

    # Export for the template build
    export TESTS_SANDBOX_TEMPLATE_ID="$TEMPLATE_ID"
    export TESTS_SANDBOX_BUILD_ID="$BUILD_ID"

    # Append to env file
    echo "TESTS_SANDBOX_TEMPLATE_ID=$TEMPLATE_ID" >> .env.localtest
    echo "TESTS_SANDBOX_BUILD_ID=$BUILD_ID" >> .env.localtest

    # Set storage path for local mode (create-build uses this as base for kernels, fc-versions, templates, etc.)
    STORAGE_ARG=""
    if [[ "$STORAGE_MODE" == "local" ]]; then
        STORAGE_ARG="STORAGE_PATH=$E2B_LOCAL_DIR"
    fi

    # Don't pass STORAGE_PROVIDER - create-build sets it correctly based on -storage flag
    sudo -E make -C packages/orchestrator build-template \
        TEMPLATE_ID="$TEMPLATE_ID" \
        BUILD_ID="$BUILD_ID" \
        KERNEL_VERSION="vmlinux-6.1.158" \
        FIRECRACKER_VERSION="v1.12.1_717921c" \
        $STORAGE_ARG

    echo "Template built."
}

# Start the services
start_services() {
    echo "=== Starting services ==="

    cd "$ROOT_DIR"
    mkdir -p "$E2B_LOCAL_DIR/logs"

    # Source the env for API (non-sudo)
    set -a
    source .env.localtest
    set +a

    # Start orchestrator with explicit env vars (sudo -E doesn't work with sudo-rs)
    echo "Starting orchestrator..."
    sudo \
        STORAGE_PROVIDER=Local \
        LOCAL_TEMPLATE_STORAGE_BASE_PATH="$E2B_LOCAL_DIR/templates" \
        LOCAL_BUILD_CACHE_STORAGE_BASE_PATH="$E2B_LOCAL_DIR/build-cache" \
        HOST_KERNELS_DIR="$E2B_LOCAL_DIR/kernels" \
        FIRECRACKER_VERSIONS_DIR="$E2B_LOCAL_DIR/fc-versions" \
        ORCHESTRATOR_BASE_PATH="$E2B_LOCAL_DIR/orchestrator" \
        SANDBOX_DIR="$E2B_LOCAL_DIR/sandbox" \
        SNAPSHOT_CACHE_DIR="$E2B_LOCAL_DIR/snapshot-cache" \
        HOST_ENVD_PATH="$E2B_LOCAL_DIR/fc-envd/envd" \
        ORCHESTRATOR_SERVICES="orchestrator,template-manager" \
        MAX_PARALLEL_MEMFILE_SNAPSHOTTING=2 \
        ENVIRONMENT=local \
        bash ./scripts/start-service.sh "Orchestrator" packages/orchestrator run-debug "$E2B_LOCAL_DIR/logs/orchestrator.log" http://localhost:5008/health &

    # Start API
    echo "Starting API..."
    bash ./scripts/start-service.sh "API" packages/api run "$E2B_LOCAL_DIR/logs/api.log" http://localhost:3000/health &

    # Wait for services
    echo "Waiting for services to be healthy..."
    for i in {1..30}; do
        if curl -s http://localhost:5008/health > /dev/null && curl -s http://localhost:3000/health > /dev/null; then
            echo "Services are healthy!"
            return 0
        fi
        sleep 2
    done

    echo "ERROR: Services failed to start. Check $E2B_LOCAL_DIR/logs/"
    return 1
}

# Run the tests
run_tests() {
    echo "=== Running integration tests ==="

    cd "$ROOT_DIR"

    if [[ -n "$TEST_NAME" ]]; then
        make -C tests/integration test/.:$TEST_NAME
    else
        make -C tests/integration test
    fi
}

# Cleanup
cleanup() {
    echo "=== Cleaning up ==="

    # Kill services
    pkill -f "packages/orchestrator" 2>/dev/null || true
    pkill -f "packages/api" 2>/dev/null || true

    echo "Cleanup complete. Dependencies (postgres, redis) left running for reuse."
    echo "Logs at: $E2B_LOCAL_DIR/logs/"
    echo "To fully cleanup: docker stop postgres redis && docker rm postgres redis"
}

# Main
main() {
    check_sudo

    if [[ "$SKIP_SETUP" == "false" ]]; then
        setup_host
        download_artifacts
        build_envd
        setup_env
        start_dependencies
        build_template
    fi

    start_services

    trap cleanup EXIT

    run_tests
}

main
