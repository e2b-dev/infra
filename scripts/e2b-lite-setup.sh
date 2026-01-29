#!/bin/bash
#
# E2B Lite Setup Script
#
# This script sets up a complete E2B Lite environment for local development.
# It handles prerequisites, downloads artifacts, builds binaries, starts
# infrastructure, and optionally builds the base template.
#
# Usage:
#   ./scripts/e2b-lite-setup.sh              # Full setup (requires sudo)
#   ./scripts/e2b-lite-setup.sh --deps-only  # Only install system dependencies
#   ./scripts/e2b-lite-setup.sh --no-deps    # Skip dependency installation
#   ./scripts/e2b-lite-setup.sh --no-template # Skip template building
#   ./scripts/e2b-lite-setup.sh --prebuilt   # Download pre-built binaries (faster)
#   ./scripts/e2b-lite-setup.sh --prebuilt --version v1.0.0  # Specific version
#
# Requirements:
#   - Linux with KVM support (bare metal recommended)
#   - Root/sudo access
#   - Internet connection
#

set -euo pipefail

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Configuration
KERNEL_VERSION="${KERNEL_VERSION:-vmlinux-6.1.158}"
FC_VERSION="${FC_VERSION:-v1.12.1_717921c}"

# Paths (relative to repo root)
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

FC_VERSIONS_DIR="$REPO_ROOT/packages/fc-versions/builds"
KERNELS_DIR="$REPO_ROOT/packages/fc-kernels"
ENVD_DIR="$REPO_ROOT/packages/envd"
API_DIR="$REPO_ROOT/packages/api"
ORCHESTRATOR_DIR="$REPO_ROOT/packages/orchestrator"
CLIENT_PROXY_DIR="$REPO_ROOT/packages/client-proxy"
SHARED_SCRIPTS_DIR="$REPO_ROOT/packages/shared/scripts"
LOCAL_DEV_DIR="$REPO_ROOT/packages/local-dev"
TMP_DIR="$REPO_ROOT/tmp"

# Download URLs
KERNEL_URL="https://storage.googleapis.com/e2b-prod-public-builds/kernels/${KERNEL_VERSION}/vmlinux.bin"
FC_URL="https://github.com/e2b-dev/fc-versions/releases/download/${FC_VERSION}/firecracker"

# Default credentials (from seed-local-database.go)
API_KEY="e2b_53ae1fed82754c17ad8077fbc8bcdd90"
ACCESS_TOKEN="sk_e2b_89215020937a4c989cde33d7bc647715"

# Parse arguments
INSTALL_DEPS=true
BUILD_TEMPLATE=true
DEPS_ONLY=false
USE_PREBUILT=false
PREBUILT_VERSION="latest"

while [[ $# -gt 0 ]]; do
    case $1 in
        --no-deps)
            INSTALL_DEPS=false
            shift
            ;;
        --deps-only)
            DEPS_ONLY=true
            shift
            ;;
        --no-template)
            BUILD_TEMPLATE=false
            shift
            ;;
        --prebuilt)
            USE_PREBUILT=true
            shift
            ;;
        --version)
            PREBUILT_VERSION="$2"
            shift 2
            ;;
        --help|-h)
            echo "Usage: $0 [options]"
            echo ""
            echo "Options:"
            echo "  --no-deps          Skip system dependency installation"
            echo "  --deps-only        Only install dependencies, then exit"
            echo "  --no-template      Skip template building"
            echo "  --prebuilt         Download pre-built binaries instead of compiling"
            echo "  --version VERSION  Specify version for pre-built binaries (default: latest)"
            echo "  --help             Show this help"
            exit 0
            ;;
        *)
            echo "Unknown option: $1"
            exit 1
            ;;
    esac
done

echo "========================================"
echo "  E2B Lite Setup"
echo "========================================"
echo ""

# -----------------------------------------------------------------------------
# Fix git safe directory (needed when repo is rsync'd/copied)
# -----------------------------------------------------------------------------
if ! git -C "$REPO_ROOT" status &>/dev/null; then
    echo "Fixing git safe directory..."
    git config --global --add safe.directory "$REPO_ROOT" 2>/dev/null || true
fi

# -----------------------------------------------------------------------------
# Check if running as root for certain operations
# -----------------------------------------------------------------------------
check_sudo() {
    if [[ $EUID -ne 0 ]]; then
        if ! sudo -n true 2>/dev/null; then
            echo -e "${YELLOW}Some operations require sudo. You may be prompted for password.${NC}"
        fi
    fi
}

# -----------------------------------------------------------------------------
# Install system dependencies
# -----------------------------------------------------------------------------
install_dependencies() {
    echo -e "${BLUE}Installing system dependencies...${NC}"
    echo ""

    # Detect package manager
    if command -v apt-get &> /dev/null; then
        PKG_MANAGER="apt"
    else
        echo -e "${RED}Error: Only apt-based systems (Ubuntu/Debian) are currently supported${NC}"
        exit 1
    fi

    # Update package list
    echo "  Updating package list..."
    sudo apt-get update -qq

    # Install Docker if not present
    if ! command -v docker &> /dev/null; then
        echo "  Installing Docker..."
        sudo install -m 0755 -d /etc/apt/keyrings
        sudo curl -fsSL https://download.docker.com/linux/ubuntu/gpg -o /etc/apt/keyrings/docker.asc
        sudo chmod a+r /etc/apt/keyrings/docker.asc

        echo "Types: deb
URIs: https://download.docker.com/linux/ubuntu
Suites: $(. /etc/os-release && echo "${UBUNTU_CODENAME:-$VERSION_CODENAME}")
Components: stable
Signed-By: /etc/apt/keyrings/docker.asc" | sudo tee /etc/apt/sources.list.d/docker.sources > /dev/null

        sudo apt-get update -qq
        sudo apt-get install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
        echo -e "  ${GREEN}✓${NC} Docker installed"
    else
        echo -e "  ${GREEN}✓${NC} Docker already installed"
    fi

    # Install Go if not present
    if ! command -v go &> /dev/null; then
        echo "  Installing Go via snap..."
        sudo snap install --classic go
        echo -e "  ${GREEN}✓${NC} Go installed"
    else
        echo -e "  ${GREEN}✓${NC} Go already installed ($(go version | grep -oP '\d+\.\d+' | head -1))"
    fi

    # Install Node.js if not present (needed for template building)
    if ! command -v node &> /dev/null; then
        echo "  Installing Node.js..."
        # Install via NodeSource for recent version
        curl -fsSL https://deb.nodesource.com/setup_22.x | sudo -E bash -
        sudo apt-get install -y nodejs
        echo -e "  ${GREEN}✓${NC} Node.js installed"
    else
        echo -e "  ${GREEN}✓${NC} Node.js already installed ($(node --version))"
    fi

    # Install build tools and other dependencies
    echo "  Installing build tools..."
    sudo apt-get install -y build-essential make ca-certificates curl git net-tools
    echo -e "  ${GREEN}✓${NC} Build tools installed"

    echo ""
}

# -----------------------------------------------------------------------------
# Check prerequisites
# -----------------------------------------------------------------------------
check_prerequisites() {
    echo "Checking prerequisites..."

    # Check OS
    if [[ "$OSTYPE" != "linux-gnu"* ]]; then
        echo -e "${RED}Error: E2B Lite requires Linux. Detected: $OSTYPE${NC}"
        exit 1
    fi
    echo -e "  ${GREEN}✓${NC} Linux detected"

    # Check kernel version
    KERNEL_MAJOR=$(uname -r | cut -d. -f1)
    KERNEL_MINOR=$(uname -r | cut -d. -f2)
    if [ "$KERNEL_MAJOR" -lt 5 ] || ([ "$KERNEL_MAJOR" -eq 5 ] && [ "$KERNEL_MINOR" -lt 10 ]); then
        echo -e "${RED}Error: Kernel $(uname -r) is too old. Minimum required: 5.10${NC}"
        exit 1
    fi
    echo -e "  ${GREEN}✓${NC} Kernel $(uname -r)"

    # Check for kernel 6.8+ (needed for building templates)
    if [ "$KERNEL_MAJOR" -lt 6 ] || ([ "$KERNEL_MAJOR" -eq 6 ] && [ "$KERNEL_MINOR" -lt 8 ]); then
        echo -e "  ${YELLOW}!${NC} Kernel < 6.8: You can run sandboxes but cannot build custom templates"
        BUILD_TEMPLATE=false
    else
        echo -e "  ${GREEN}✓${NC} Kernel 6.8+: Full support (running + building templates)"
    fi

    # Check KVM
    if [[ ! -e /dev/kvm ]]; then
        echo -e "${RED}Error: /dev/kvm not found. KVM is required.${NC}"
        echo "  Enable KVM: sudo modprobe kvm_intel (or kvm_amd)"
        exit 1
    fi
    if [[ ! -r /dev/kvm ]] || [[ ! -w /dev/kvm ]]; then
        echo -e "${YELLOW}Warning: No read/write access to /dev/kvm${NC}"
        echo "  Fix: sudo usermod -aG kvm \$USER && newgrp kvm"
        echo "  Or run with sudo"
    fi
    echo -e "  ${GREEN}✓${NC} KVM available"

    # Check Docker
    if ! command -v docker &> /dev/null; then
        echo -e "${RED}Error: Docker not found. Run with --deps-only first or install manually.${NC}"
        exit 1
    fi
    if ! docker info &> /dev/null; then
        echo -e "${RED}Error: Docker daemon not running or no permission${NC}"
        echo "  Start Docker: sudo systemctl start docker"
        echo "  Or add to group: sudo usermod -aG docker \$USER && newgrp docker"
        exit 1
    fi
    echo -e "  ${GREEN}✓${NC} Docker available"

    # Check Go
    if ! command -v go &> /dev/null; then
        echo -e "${RED}Error: Go not found. Run with --deps-only first or install manually.${NC}"
        exit 1
    fi
    GO_VERSION=$(go version | grep -oP '\d+\.\d+' | head -1)
    echo -e "  ${GREEN}✓${NC} Go $GO_VERSION"

    # Check Node.js (optional, for template building)
    if command -v node &> /dev/null; then
        echo -e "  ${GREEN}✓${NC} Node.js $(node --version)"
    else
        echo -e "  ${YELLOW}!${NC} Node.js not found (needed for template building)"
    fi

    echo ""
}

# -----------------------------------------------------------------------------
# Setup kernel modules
# -----------------------------------------------------------------------------
setup_kernel_modules() {
    echo "Setting up kernel modules..."

    # NBD module with sufficient devices
    if ! lsmod | grep -q "^nbd "; then
        echo "  Loading NBD module..."
        if ! sudo modprobe nbd nbds_max=128 2>/dev/null; then
            echo -e "${YELLOW}Warning: Failed to load NBD module. You may need to install it.${NC}"
        else
            echo -e "  ${GREEN}✓${NC} NBD module loaded (nbds_max=128)"
        fi
    else
        # Check if we have enough NBD devices
        NBD_COUNT=$(ls -1 /dev/nbd* 2>/dev/null | wc -l)
        if [ "$NBD_COUNT" -lt 64 ]; then
            echo "  Reloading NBD module with more devices..."
            sudo rmmod nbd 2>/dev/null || true
            sudo modprobe nbd nbds_max=128
        fi
        echo -e "  ${GREEN}✓${NC} NBD module loaded"
    fi

    # TUN module
    if ! lsmod | grep -q "^tun "; then
        echo "  Loading TUN module..."
        sudo modprobe tun 2>/dev/null || echo -e "${YELLOW}Warning: Failed to load TUN module${NC}"
    fi
    echo -e "  ${GREEN}✓${NC} TUN module"

    echo ""
}

# -----------------------------------------------------------------------------
# Setup HugePages
# -----------------------------------------------------------------------------
setup_hugepages() {
    echo "Setting up HugePages..."

    HUGEPAGES_TOTAL=$(cat /proc/sys/vm/nr_hugepages 2>/dev/null || echo 0)
    HUGEPAGES_NEEDED=2048  # 2048 * 2MB = 4GB reserved for HugePages

    if [ "$HUGEPAGES_TOTAL" -lt "$HUGEPAGES_NEEDED" ]; then
        echo "  Allocating HugePages ($HUGEPAGES_NEEDED pages = $((HUGEPAGES_NEEDED * 2))MB)..."
        if echo "$HUGEPAGES_NEEDED" | sudo tee /proc/sys/vm/nr_hugepages > /dev/null 2>&1; then
            # Make it persistent across reboots
            if ! grep -q "vm.nr_hugepages" /etc/sysctl.conf 2>/dev/null; then
                echo "vm.nr_hugepages=$HUGEPAGES_NEEDED" | sudo tee -a /etc/sysctl.conf > /dev/null
                echo -e "  ${GREEN}✓${NC} HugePages configured (persistent)"
            else
                sudo sed -i "s/vm.nr_hugepages=.*/vm.nr_hugepages=$HUGEPAGES_NEEDED/" /etc/sysctl.conf
                echo -e "  ${GREEN}✓${NC} HugePages allocated"
            fi
        else
            echo -e "${YELLOW}Warning: Failed to allocate HugePages. Template building may fail.${NC}"
            echo "  Manual fix: echo $HUGEPAGES_NEEDED | sudo tee /proc/sys/vm/nr_hugepages"
        fi
    else
        echo -e "  ${GREEN}✓${NC} HugePages already configured ($HUGEPAGES_TOTAL pages)"
    fi

    echo ""
}

# -----------------------------------------------------------------------------
# Create directory structure
# -----------------------------------------------------------------------------
create_directories() {
    echo "Creating directory structure..."

    mkdir -p "$FC_VERSIONS_DIR/$FC_VERSION"
    mkdir -p "$KERNELS_DIR/$KERNEL_VERSION"
    mkdir -p "$TMP_DIR/templates"
    mkdir -p "$TMP_DIR/orchestrator"
    mkdir -p "$TMP_DIR/sandbox"
    mkdir -p "$TMP_DIR/sandbox-cache"
    mkdir -p "$TMP_DIR/snapshot-cache"
    mkdir -p "$ORCHESTRATOR_DIR/tmp/local-template-storage"
    mkdir -p "$ORCHESTRATOR_DIR/tmp/sandbox-cache-dir"
    mkdir -p "$ORCHESTRATOR_DIR/tmp/snapshot-cache"

    echo -e "  ${GREEN}✓${NC} Directories created"
    echo ""
}

# -----------------------------------------------------------------------------
# Download artifacts
# -----------------------------------------------------------------------------
download_artifacts() {
    echo "Downloading artifacts..."

    # Download kernel
    KERNEL_PATH="$KERNELS_DIR/$KERNEL_VERSION/vmlinux.bin"
    if [[ -f "$KERNEL_PATH" ]]; then
        echo -e "  ${GREEN}✓${NC} Kernel $KERNEL_VERSION already exists"
    else
        echo "  Downloading kernel $KERNEL_VERSION..."
        if curl -fsSL "$KERNEL_URL" -o "$KERNEL_PATH"; then
            chmod 644 "$KERNEL_PATH"
            echo -e "  ${GREEN}✓${NC} Kernel downloaded"
        else
            echo -e "${RED}Error: Failed to download kernel${NC}"
            echo "  URL: $KERNEL_URL"
            exit 1
        fi
    fi

    # Download Firecracker
    FC_PATH="$FC_VERSIONS_DIR/$FC_VERSION/firecracker"
    if [[ -f "$FC_PATH" ]]; then
        echo -e "  ${GREEN}✓${NC} Firecracker $FC_VERSION already exists"
    else
        echo "  Downloading Firecracker $FC_VERSION..."
        if curl -fsSL "$FC_URL" -o "$FC_PATH"; then
            chmod +x "$FC_PATH"
            echo -e "  ${GREEN}✓${NC} Firecracker downloaded"
        else
            echo -e "${RED}Error: Failed to download Firecracker${NC}"
            echo "  URL: $FC_URL"
            exit 1
        fi
    fi

    echo ""
}

# -----------------------------------------------------------------------------
# Download pre-built binaries
# -----------------------------------------------------------------------------
download_prebuilt_binaries() {
    echo "Downloading pre-built binaries..."

    GITHUB_REPO="e2b-dev/infra"

    # Determine version to download
    if [[ "$PREBUILT_VERSION" == "latest" ]]; then
        echo "  Fetching latest release..."
        RELEASE_URL="https://api.github.com/repos/$GITHUB_REPO/releases/latest"
        RELEASE_INFO=$(curl -fsSL "$RELEASE_URL" 2>/dev/null)
        if [[ -z "$RELEASE_INFO" ]]; then
            echo -e "${RED}Error: Failed to fetch latest release info${NC}"
            echo "  Falling back to building from source..."
            build_binaries
            return
        fi
        VERSION=$(echo "$RELEASE_INFO" | grep -oP '"tag_name":\s*"\K[^"]+' | head -1)
        if [[ -z "$VERSION" ]]; then
            echo -e "${YELLOW}Warning: No releases found, falling back to building from source${NC}"
            build_binaries
            return
        fi
    else
        VERSION="$PREBUILT_VERSION"
    fi

    echo "  Version: $VERSION"

    # Download URLs
    BASE_URL="https://github.com/$GITHUB_REPO/releases/download/$VERSION"

    # Create bin directories
    mkdir -p "$API_DIR/bin"
    mkdir -p "$ORCHESTRATOR_DIR/bin"
    mkdir -p "$CLIENT_PROXY_DIR/bin"
    mkdir -p "$ENVD_DIR/bin"

    # Download API
    API_PATH="$API_DIR/bin/api"
    if [[ -f "$API_PATH" ]]; then
        echo -e "  ${GREEN}✓${NC} API already exists"
    else
        echo "  Downloading API..."
        if curl -fsSL "$BASE_URL/api-linux-amd64" -o "$API_PATH"; then
            chmod +x "$API_PATH"
            echo -e "  ${GREEN}✓${NC} API downloaded"
        else
            echo -e "${YELLOW}Warning: Failed to download API, will build from source${NC}"
            BUILD_API=true
        fi
    fi

    # Download Orchestrator
    ORCH_PATH="$ORCHESTRATOR_DIR/bin/orchestrator"
    if [[ -f "$ORCH_PATH" ]]; then
        echo -e "  ${GREEN}✓${NC} Orchestrator already exists"
    else
        echo "  Downloading Orchestrator..."
        if curl -fsSL "$BASE_URL/orchestrator-linux-amd64" -o "$ORCH_PATH"; then
            chmod +x "$ORCH_PATH"
            echo -e "  ${GREEN}✓${NC} Orchestrator downloaded"
        else
            echo -e "${YELLOW}Warning: Failed to download Orchestrator, will build from source${NC}"
            BUILD_ORCH=true
        fi
    fi

    # Download Client-Proxy
    PROXY_PATH="$CLIENT_PROXY_DIR/bin/client-proxy"
    if [[ -f "$PROXY_PATH" ]]; then
        echo -e "  ${GREEN}✓${NC} Client-Proxy already exists"
    else
        echo "  Downloading Client-Proxy..."
        if curl -fsSL "$BASE_URL/client-proxy-linux-amd64" -o "$PROXY_PATH"; then
            chmod +x "$PROXY_PATH"
            echo -e "  ${GREEN}✓${NC} Client-Proxy downloaded"
        else
            echo -e "${YELLOW}Warning: Failed to download Client-Proxy, will build from source${NC}"
            BUILD_PROXY=true
        fi
    fi

    # Download Envd
    ENVD_PATH="$ENVD_DIR/bin/envd"
    if [[ -f "$ENVD_PATH" ]]; then
        echo -e "  ${GREEN}✓${NC} envd already exists"
    else
        echo "  Downloading envd..."
        if curl -fsSL "$BASE_URL/envd-linux-amd64" -o "$ENVD_PATH"; then
            chmod +x "$ENVD_PATH"
            echo -e "  ${GREEN}✓${NC} envd downloaded"
        else
            echo -e "${YELLOW}Warning: Failed to download envd, will build from source${NC}"
            BUILD_ENVD=true
        fi
    fi

    # Build any that failed to download
    if [[ "$BUILD_API" == "true" ]] || [[ "$BUILD_ORCH" == "true" ]] || \
       [[ "$BUILD_PROXY" == "true" ]] || [[ "$BUILD_ENVD" == "true" ]]; then
        echo ""
        echo "Building missing binaries from source..."

        if [[ "$BUILD_ENVD" == "true" ]]; then
            echo "  Building envd..."
            make -C "$ENVD_DIR" build > /dev/null 2>&1 || echo -e "${RED}Failed to build envd${NC}"
        fi

        if [[ "$BUILD_API" == "true" ]]; then
            echo "  Building API..."
            make -C "$API_DIR" build > /dev/null 2>&1 || echo -e "${RED}Failed to build API${NC}"
        fi

        if [[ "$BUILD_ORCH" == "true" ]]; then
            echo "  Building Orchestrator..."
            make -C "$ORCHESTRATOR_DIR" build-debug > /dev/null 2>&1 || echo -e "${RED}Failed to build Orchestrator${NC}"
        fi

        if [[ "$BUILD_PROXY" == "true" ]]; then
            echo "  Building Client-Proxy..."
            make -C "$CLIENT_PROXY_DIR" build > /dev/null 2>&1 || echo -e "${RED}Failed to build Client-Proxy${NC}"
        fi
    fi

    echo ""
}

# -----------------------------------------------------------------------------
# Build all binaries
# -----------------------------------------------------------------------------
build_binaries() {
    echo "Building binaries..."

    # Build envd
    ENVD_DEBUG_PATH="$ENVD_DIR/bin/debug/envd"
    ENVD_PATH="$ENVD_DIR/bin/envd"
    if [[ -f "$ENVD_DEBUG_PATH" ]]; then
        echo -e "  ${GREEN}✓${NC} envd already built"
    else
        echo "  Building envd..."
        if make -C "$ENVD_DIR" build-debug > /dev/null 2>&1; then
            echo -e "  ${GREEN}✓${NC} envd built"
        else
            echo -e "${RED}Error: Failed to build envd${NC}"
            exit 1
        fi
    fi

    # Create symlink for envd
    if [[ ! -L "$ENVD_PATH" ]] && [[ ! -f "$ENVD_PATH" ]]; then
        ln -s "$ENVD_DEBUG_PATH" "$ENVD_PATH"
        echo -e "  ${GREEN}✓${NC} envd symlink created"
    fi

    # Build API
    API_PATH="$API_DIR/bin/api"
    if [[ -f "$API_PATH" ]]; then
        echo -e "  ${GREEN}✓${NC} API already built"
    else
        echo "  Building API..."
        if make -C "$API_DIR" build > /dev/null 2>&1; then
            echo -e "  ${GREEN}✓${NC} API built"
        else
            echo -e "${RED}Error: Failed to build API${NC}"
            exit 1
        fi
    fi

    # Build Orchestrator
    ORCH_PATH="$ORCHESTRATOR_DIR/bin/orchestrator"
    if [[ -f "$ORCH_PATH" ]]; then
        echo -e "  ${GREEN}✓${NC} Orchestrator already built"
    else
        echo "  Building Orchestrator..."
        if make -C "$ORCHESTRATOR_DIR" build-debug > /dev/null 2>&1; then
            echo -e "  ${GREEN}✓${NC} Orchestrator built"
        else
            echo -e "${RED}Error: Failed to build Orchestrator${NC}"
            exit 1
        fi
    fi

    # Build Client-Proxy
    PROXY_PATH="$CLIENT_PROXY_DIR/bin/client-proxy"
    if [[ -f "$PROXY_PATH" ]]; then
        echo -e "  ${GREEN}✓${NC} Client-Proxy already built"
    else
        echo "  Building Client-Proxy..."
        if make -C "$CLIENT_PROXY_DIR" build > /dev/null 2>&1; then
            echo -e "  ${GREEN}✓${NC} Client-Proxy built"
        else
            echo -e "${RED}Error: Failed to build Client-Proxy${NC}"
            exit 1
        fi
    fi

    echo ""
}

# -----------------------------------------------------------------------------
# Setup npm dependencies for template building
# -----------------------------------------------------------------------------
setup_npm_dependencies() {
    if ! command -v npm &> /dev/null; then
        echo -e "${YELLOW}Skipping npm dependencies (npm not found)${NC}"
        return
    fi

    echo "Setting up npm dependencies..."

    if [[ -d "$SHARED_SCRIPTS_DIR" ]]; then
        if [[ ! -d "$SHARED_SCRIPTS_DIR/node_modules" ]]; then
            echo "  Installing npm packages in shared/scripts..."
            (cd "$SHARED_SCRIPTS_DIR" && npm install --silent) || {
                echo -e "${YELLOW}Warning: Failed to install npm packages${NC}"
            }
        fi
        echo -e "  ${GREEN}✓${NC} npm dependencies ready"
    fi

    echo ""
}

# -----------------------------------------------------------------------------
# Start Docker infrastructure
# -----------------------------------------------------------------------------
start_infrastructure() {
    echo "Starting Docker infrastructure..."

    # Use full docker-compose with all services
    COMPOSE_FILE="$LOCAL_DEV_DIR/docker-compose.yaml"

    if [[ ! -f "$COMPOSE_FILE" ]]; then
        echo -e "${RED}Error: docker-compose.yaml not found at $COMPOSE_FILE${NC}"
        exit 1
    fi

    # Check if containers are already running
    if docker ps --format '{{.Names}}' | grep -q "local-dev-postgres"; then
        echo -e "  ${GREEN}✓${NC} Infrastructure already running"
    else
        echo "  Starting containers..."
        docker compose -f "$COMPOSE_FILE" up -d

        # Wait for PostgreSQL to be ready
        echo "  Waiting for PostgreSQL..."
        for i in {1..30}; do
            if docker exec local-dev-postgres-1 pg_isready -U postgres > /dev/null 2>&1; then
                break
            fi
            sleep 1
        done
        echo -e "  ${GREEN}✓${NC} Infrastructure started"
    fi

    echo ""
}

# -----------------------------------------------------------------------------
# Run database migrations
# -----------------------------------------------------------------------------
run_migrations() {
    echo "Running database migrations..."

    export POSTGRES_CONNECTION_STRING="postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable"

    if make -C "$REPO_ROOT/packages/db" migrate-local > /dev/null 2>&1; then
        echo -e "  ${GREEN}✓${NC} Migrations applied"
    else
        echo -e "${YELLOW}Warning: Migration may have failed or already applied${NC}"
    fi

    echo ""
}

# -----------------------------------------------------------------------------
# Seed database
# -----------------------------------------------------------------------------
seed_database() {
    echo "Seeding database..."

    export POSTGRES_CONNECTION_STRING="postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable"

    # Check if already seeded by looking for the team
    TEAM_EXISTS=$(docker exec local-dev-postgres-1 psql -U postgres -tAc "SELECT COUNT(*) FROM teams WHERE id='0b8a3ded-4489-4722-afd1-1d82e64ec2d5';" 2>/dev/null || echo "0")

    if [[ "$TEAM_EXISTS" == "1" ]]; then
        echo -e "  ${GREEN}✓${NC} Database already seeded"
    else
        echo "  Running seed script..."
        (cd "$LOCAL_DEV_DIR" && go run seed-local-database.go) > /dev/null 2>&1 || {
            echo -e "${YELLOW}Warning: Seeding may have failed${NC}"
        }
        echo -e "  ${GREEN}✓${NC} Database seeded"
    fi

    echo ""
}

# -----------------------------------------------------------------------------
# Build base template
# -----------------------------------------------------------------------------

# Generate a 20-character lowercase alphanumeric template ID (like e2b production)
generate_template_id() {
    # Use head -c to read finite bytes first (avoids SIGPIPE with pipefail)
    head -c 500 /dev/urandom | tr -dc 'a-z0-9' | head -c 20
    echo  # Add newline
}

build_base_template() {
    if [[ "$BUILD_TEMPLATE" != "true" ]]; then
        echo -e "${YELLOW}Skipping template build (--no-template or kernel < 6.8)${NC}"
        echo ""
        return
    fi

    echo "Building base template..."

    # Check if template already exists in database
    EXISTING_TEMPLATE=$(docker exec local-dev-postgres-1 psql -U postgres -tAc "SELECT id FROM envs LIMIT 1;" 2>/dev/null | tr -d ' ' || echo "")
    if [[ -n "$EXISTING_TEMPLATE" ]]; then
        echo -e "  ${GREEN}✓${NC} Template already exists in database: $EXISTING_TEMPLATE"
        echo ""
        return
    fi

    # Check if template files exist but aren't registered
    TEMPLATE_STORAGE="$ORCHESTRATOR_DIR/tmp/local-template-storage"
    EXISTING_BUILD=$(ls -1 "$TEMPLATE_STORAGE" 2>/dev/null | head -1)

    if [[ -n "$EXISTING_BUILD" ]]; then
        echo "  Found existing template files, registering in database..."
        TEMPLATE_ID=$(generate_template_id)
        BUILD_ID="$EXISTING_BUILD"
        register_template "$TEMPLATE_ID" "$BUILD_ID"
        echo -e "  ${GREEN}✓${NC} Template registered: $TEMPLATE_ID"
        echo ""
        return
    fi

    # Set environment for template building
    export STORAGE_PROVIDER=Local
    export ARTIFACTS_REGISTRY_PROVIDER=Local
    export LOCAL_TEMPLATE_STORAGE_BASE_PATH="$TEMPLATE_STORAGE"
    export HOST_ENVD_PATH="$ENVD_DIR/bin/envd"
    export HOST_KERNELS_DIR="$KERNELS_DIR"
    export FIRECRACKER_VERSIONS_DIR="$FC_VERSIONS_DIR"
    export ORCHESTRATOR_BASE_PATH="$ORCHESTRATOR_DIR/tmp"
    export SANDBOX_DIR="$ORCHESTRATOR_DIR/tmp/sandbox"
    export POSTGRES_CONNECTION_STRING="postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable"

    # Generate IDs
    TEMPLATE_ID=$(generate_template_id)
    BUILD_ID=$(cat /proc/sys/kernel/random/uuid)

    echo "  Template ID: $TEMPLATE_ID"
    echo "  Build ID: $BUILD_ID"
    echo "  Building template (this may take a few minutes)..."

    if go run "$ORCHESTRATOR_DIR/cmd/create-build/main.go" \
        -template "$TEMPLATE_ID" \
        -to-build "$BUILD_ID" \
        -storage "$ORCHESTRATOR_DIR/tmp" \
        -kernel "$KERNEL_VERSION" \
        -firecracker "$FC_VERSION" \
        -vcpu 2 \
        -memory 512 \
        -disk 1024 > /tmp/template-build.log 2>&1; then
        echo -e "  ${GREEN}✓${NC} Template built successfully"

        # Register template in database
        register_template "$TEMPLATE_ID" "$BUILD_ID"
        echo -e "  ${GREEN}✓${NC} Template registered in database"
    else
        echo -e "${YELLOW}Warning: Template build failed. Check /tmp/template-build.log${NC}"
        echo "  You can build it manually later with:"
        echo "  make -C packages/shared/scripts local-build-base-template"
    fi

    echo ""
}

# Register template in the database
register_template() {
    local TEMPLATE_ID="$1"
    local BUILD_ID="$2"
    local TEAM_ID="0b8a3ded-4489-4722-afd1-1d82e64ec2d5"

    # Insert into envs table
    docker exec local-dev-postgres-1 psql -U postgres -c "
        INSERT INTO public.envs (id, team_id, public, updated_at)
        VALUES ('$TEMPLATE_ID', '$TEAM_ID', true, NOW())
        ON CONFLICT (id) DO NOTHING;
    " > /dev/null 2>&1

    # Insert into env_builds table (status must be 'uploaded' for API to find it)
    # Note: total_disk_size_mb and envd_version are required by the API
    docker exec local-dev-postgres-1 psql -U postgres -c "
        INSERT INTO public.env_builds (id, env_id, status, vcpu, ram_mb, free_disk_size_mb, total_disk_size_mb, kernel_version, firecracker_version, envd_version, cluster_node_id, created_at, updated_at, finished_at)
        VALUES ('$BUILD_ID', '$TEMPLATE_ID', 'uploaded', 2, 512, 1024, 512, '$KERNEL_VERSION', '$FC_VERSION', '0.2.0', 'local', NOW(), NOW(), NOW())
        ON CONFLICT (id) DO NOTHING;
    " > /dev/null 2>&1

    # Insert into env_build_assignments table (links build to template with 'default' tag)
    docker exec local-dev-postgres-1 psql -U postgres -c "
        INSERT INTO public.env_build_assignments (env_id, build_id, tag, source, created_at)
        VALUES ('$TEMPLATE_ID', '$BUILD_ID', 'default', 'setup', NOW())
        ON CONFLICT DO NOTHING;
    " > /dev/null 2>&1
}

# -----------------------------------------------------------------------------
# Create service start scripts
# -----------------------------------------------------------------------------
create_start_scripts() {
    echo "Creating service start scripts..."

    # Create scripts directory
    mkdir -p "$REPO_ROOT/scripts/services"

    # API start script
    cat > "$REPO_ROOT/scripts/services/start-api.sh" << 'SCRIPT'
#!/bin/bash
cd "$(dirname "$0")/../.." || exit 1
REPO_ROOT="$(pwd)"

cd packages/api || exit 1

NODE_ID=$(hostname) \
LOKI_URL="localhost:3100" \
DNS_PORT=9953 \
ENVIRONMENT=local \
LOGS_COLLECTOR_ADDRESS=http://localhost:30006 \
OTEL_COLLECTOR_GRPC_ENDPOINT=localhost:4317 \
POSTGRES_CONNECTION_STRING="postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable" \
REDIS_URL=localhost:6379 \
SANDBOX_ACCESS_TOKEN_HASH_SEED="--sandbox-access-token-hash-seed--" \
LOCAL_CLUSTER_ENDPOINT=localhost:3001 \
LOCAL_CLUSTER_TOKEN="--edge-secret--" \
./bin/api
SCRIPT
    chmod +x "$REPO_ROOT/scripts/services/start-api.sh"

    # Orchestrator start script
    cat > "$REPO_ROOT/scripts/services/start-orchestrator.sh" << 'SCRIPT'
#!/bin/bash
cd "$(dirname "$0")/../.." || exit 1
REPO_ROOT="$(pwd)"

cd packages/orchestrator || exit 1

# Get absolute path to orchestrator directory
ORCH_DIR="$(pwd)"

NODE_ID=$(hostname) \
LOKI_URL="localhost:3100" \
ARTIFACTS_REGISTRY_PROVIDER=Local \
ENVIRONMENT=local \
FIRECRACKER_VERSIONS_DIR=../fc-versions/builds \
HOST_ENVD_PATH=../envd/bin/envd \
HOST_KERNELS_DIR=../fc-kernels \
LOCAL_TEMPLATE_STORAGE_BASE_PATH=./tmp/local-template-storage \
LOGS_COLLECTOR_ADDRESS=http://localhost:30006 \
ORCHESTRATOR_BASE_PATH=./tmp/ \
ORCHESTRATOR_LOCK_PATH=./tmp/.lock \
ORCHESTRATOR_SERVICES=orchestrator,template-manager \
OTEL_COLLECTOR_GRPC_ENDPOINT=localhost:4317 \
REDIS_URL=localhost:6379 \
SANDBOX_CACHE_DIR=./tmp/sandbox-cache-dir \
SANDBOX_DIR="${ORCH_DIR}/tmp/sandbox" \
SNAPSHOT_CACHE_DIR=./tmp/snapshot-cache \
STORAGE_PROVIDER=Local \
./bin/orchestrator
SCRIPT
    chmod +x "$REPO_ROOT/scripts/services/start-orchestrator.sh"

    # Client-Proxy start script
    cat > "$REPO_ROOT/scripts/services/start-client-proxy.sh" << 'SCRIPT'
#!/bin/bash
cd "$(dirname "$0")/../.." || exit 1
REPO_ROOT="$(pwd)"

cd packages/client-proxy || exit 1

NODE_ID=$(hostname) \
EDGE_SECRET="--edge-secret--" \
EDGE_URL="http://localhost:80" \
ENVIRONMENT=local \
LOGS_COLLECTOR_ADDRESS="http://localhost:30006" \
LOKI_URL="http://localhost:3100" \
NODE_IP="127.0.0.1" \
REDIS_URL="localhost:6379" \
SD_EDGE_PROVIDER=STATIC \
SD_EDGE_STATIC="127.0.0.1" \
SD_ORCHESTRATOR_PROVIDER=STATIC \
SD_ORCHESTRATOR_STATIC="127.0.0.1" \
SKIP_ORCHESTRATOR_READINESS_CHECK=true \
OTEL_COLLECTOR_GRPC_ENDPOINT=localhost:4317 \
./bin/client-proxy
SCRIPT
    chmod +x "$REPO_ROOT/scripts/services/start-client-proxy.sh"

    # All-in-one start script
    cat > "$REPO_ROOT/scripts/services/start-all.sh" << 'SCRIPT'
#!/bin/bash
#
# Start all E2B Lite services
#
# Usage:
#   ./scripts/services/start-all.sh           # Start in foreground (Ctrl+C to stop)
#   ./scripts/services/start-all.sh --bg      # Start in background
#

cd "$(dirname "$0")/../.." || exit 1
REPO_ROOT="$(pwd)"

BACKGROUND=false
if [[ "$1" == "--bg" ]]; then
    BACKGROUND=true
fi

echo "Starting E2B Lite services..."

if [[ "$BACKGROUND" == "true" ]]; then
    # Background mode
    nohup "$REPO_ROOT/scripts/services/start-api.sh" > /tmp/e2b-api.log 2>&1 &
    echo "  API started (PID: $!, log: /tmp/e2b-api.log)"

    nohup "$REPO_ROOT/scripts/services/start-orchestrator.sh" > /tmp/e2b-orchestrator.log 2>&1 &
    echo "  Orchestrator started (PID: $!, log: /tmp/e2b-orchestrator.log)"

    sleep 2  # Wait for orchestrator to initialize

    nohup "$REPO_ROOT/scripts/services/start-client-proxy.sh" > /tmp/e2b-client-proxy.log 2>&1 &
    echo "  Client-Proxy started (PID: $!, log: /tmp/e2b-client-proxy.log)"

    echo ""
    echo "All services started in background."
    echo "Check status: ps aux | grep -E 'api|orchestrator|client-proxy'"
    echo "Stop all: pkill -f 'bin/(api|orchestrator|client-proxy)'"
else
    # Foreground mode with trap
    cleanup() {
        echo ""
        echo "Stopping services..."
        pkill -f 'bin/api' 2>/dev/null
        pkill -f 'bin/orchestrator' 2>/dev/null
        pkill -f 'bin/client-proxy' 2>/dev/null
        exit 0
    }
    trap cleanup SIGINT SIGTERM

    "$REPO_ROOT/scripts/services/start-api.sh" &
    API_PID=$!
    echo "  API started (PID: $API_PID)"

    "$REPO_ROOT/scripts/services/start-orchestrator.sh" &
    ORCH_PID=$!
    echo "  Orchestrator started (PID: $ORCH_PID)"

    sleep 2

    "$REPO_ROOT/scripts/services/start-client-proxy.sh" &
    PROXY_PID=$!
    echo "  Client-Proxy started (PID: $PROXY_PID)"

    echo ""
    echo "All services running. Press Ctrl+C to stop."
    wait
fi
SCRIPT
    chmod +x "$REPO_ROOT/scripts/services/start-all.sh"

    echo -e "  ${GREEN}✓${NC} Service scripts created"
    echo ""
}

# -----------------------------------------------------------------------------
# Create test script
# -----------------------------------------------------------------------------
create_test_script() {
    echo "Creating test script..."

    cat > "$REPO_ROOT/scripts/test-e2b-lite.py" << 'SCRIPT'
#!/usr/bin/env python3
"""
E2B Lite Test Script

Tests basic sandbox functionality: creation, commands, filesystem.

Usage:
    pip install e2b
    python scripts/test-e2b-lite.py
"""

import os
import subprocess
import sys

try:
    from e2b import Sandbox
except ImportError:
    print("Error: e2b package not installed")
    print("Install with: pip install e2b")
    sys.exit(1)


def get_template_id_from_db():
    """Query PostgreSQL for the template ID."""
    try:
        result = subprocess.run(
            ["docker", "exec", "local-dev-postgres-1", "psql", "-U", "postgres", "-tAc", "SELECT id FROM envs LIMIT 1;"],
            capture_output=True,
            text=True,
            timeout=10,
        )
        if result.returncode == 0 and result.stdout.strip():
            return result.stdout.strip()
    except Exception:
        pass
    return None


# Configuration
API_KEY = os.environ.get("E2B_API_KEY", "e2b_53ae1fed82754c17ad8077fbc8bcdd90")
API_URL = os.environ.get("E2B_API_URL", "http://localhost:80")
SANDBOX_URL = os.environ.get("E2B_SANDBOX_URL", "http://localhost:3002")

# Get template ID: from env var, from database, or fail
TEMPLATE_ID = os.environ.get("E2B_TEMPLATE_ID")
if not TEMPLATE_ID:
    TEMPLATE_ID = get_template_id_from_db()

if not TEMPLATE_ID:
    print("=" * 50)
    print("  E2B Lite Test - ERROR")
    print("=" * 50)
    print()
    print("No template found!")
    print()
    print("Either:")
    print("  1. Set E2B_TEMPLATE_ID environment variable")
    print("  2. Build a template: ./scripts/e2b-lite-setup.sh")
    print()
    print("To check database:")
    print("  docker exec local-dev-postgres-1 psql -U postgres -c 'SELECT id FROM envs;'")
    sys.exit(1)

print("=" * 50)
print("  E2B Lite Test")
print("=" * 50)
print()
print(f"API URL:     {API_URL}")
print(f"Sandbox URL: {SANDBOX_URL}")
print(f"Template:    {TEMPLATE_ID}")
print()

try:
    print("1. Creating sandbox...")
    sandbox = Sandbox.create(
        template=TEMPLATE_ID,
        api_url=API_URL,
        sandbox_url=SANDBOX_URL,
        timeout=120,
        api_key=API_KEY,
    )
    print(f"   ✓ Sandbox ID: {sandbox.sandbox_id}")
    print()

    print("2. Running command...")
    result = sandbox.commands.run("echo 'Hello from E2B Lite!' && uname -a", user="root")
    print(f"   ✓ Output: {result.stdout.strip()}")
    print()

    print("3. Writing file via command...")
    sandbox.commands.run("echo 'Hello World from E2B!' > /tmp/test.txt", user="root")
    print("   ✓ Written /tmp/test.txt")
    print()

    print("4. Reading file via command...")
    result = sandbox.commands.run("cat /tmp/test.txt", user="root")
    print(f"   ✓ Content: {result.stdout.strip()}")
    print()

    print("5. Listing directory via command...")
    result = sandbox.commands.run("ls /tmp | head -5", user="root")
    print(f"   ✓ Files: {result.stdout.strip()}")
    print()

    print("6. Running Python...")
    result = sandbox.commands.run("python3 -c \"print(2+2)\"", user="root")
    print(f"   ✓ 2+2 = {result.stdout.strip()}")
    print()

    sandbox.kill()
    print("=" * 50)
    print("  All tests passed!")
    print("=" * 50)

except Exception as e:
    print(f"\n❌ Error: {e}")
    print("\nTroubleshooting:")
    print("  1. Ensure all services are running:")
    print("     ./scripts/services/start-all.sh")
    print("  2. Check service logs:")
    print("     tail -f /tmp/e2b-*.log")
    print("  3. Verify template exists in database:")
    print("     docker exec local-dev-postgres-1 psql -U postgres -c 'SELECT id FROM envs;'")
    sys.exit(1)
SCRIPT
    chmod +x "$REPO_ROOT/scripts/test-e2b-lite.py"

    echo -e "  ${GREEN}✓${NC} Test script created"
    echo ""
}

# -----------------------------------------------------------------------------
# Summary
# -----------------------------------------------------------------------------
print_summary() {
    # Get template ID from database
    TEMPLATE_ID=$(docker exec local-dev-postgres-1 psql -U postgres -tAc "SELECT id FROM envs LIMIT 1;" 2>/dev/null | tr -d ' ' || echo "")

    echo "========================================"
    echo -e "  ${GREEN}E2B Lite Setup Complete!${NC}"
    echo "========================================"
    echo ""
    echo -e "${BLUE}Credentials:${NC}"
    echo "  API Key:       $API_KEY"
    echo "  Access Token:  $ACCESS_TOKEN"
    if [[ -n "$TEMPLATE_ID" ]]; then
        echo "  Template ID:   $TEMPLATE_ID"
    fi
    echo ""
    echo -e "${BLUE}Services:${NC}"
    echo "  API:           http://localhost:80"
    echo "  Client-Proxy:  http://localhost:3002 (envd)"
    echo "  Orchestrator:  localhost:5008 (gRPC)"
    echo ""
    echo -e "${BLUE}Quick Start:${NC}"
    echo ""
    echo "  1. Start all services:"
    echo "     ./scripts/services/start-all.sh"
    echo ""
    echo "  2. Or start in background:"
    echo "     ./scripts/services/start-all.sh --bg"
    echo ""
    echo "  3. Test with Python SDK:"
    echo "     python3 -m venv e2b_venv"
    echo "     source e2b_venv/bin/activate"
    echo "     pip install e2b"
    echo "     python scripts/test-e2b-lite.py"
    echo ""
    echo -e "${BLUE}Python SDK Usage:${NC}"
    echo ""
    echo "  from e2b import Sandbox"
    echo ""
    echo "  sandbox = Sandbox.create("
    echo "      template=\"${TEMPLATE_ID:-<template_id>}\","
    echo "      api_url=\"http://localhost:80\","
    echo "      sandbox_url=\"http://localhost:3002\","
    echo "      api_key=\"$API_KEY\","
    echo "  )"
    echo "  result = sandbox.commands.run(\"echo hello\", user=\"root\")"
    echo "  print(result.stdout)"
    echo "  sandbox.kill()"
    echo ""
    echo -e "${BLUE}Environment Variables:${NC}"
    echo ""
    echo "  # For SDK"
    echo "  export E2B_API_KEY=\"$API_KEY\""
    echo ""
    echo "  # For CLI (npx @e2b/cli)"
    echo "  export E2B_API_URL=\"http://localhost:80\""
    echo "  export E2B_SANDBOX_URL=\"http://localhost:3002\""
    echo "  export E2B_ACCESS_TOKEN=\"$ACCESS_TOKEN\""
    echo "  export E2B_API_KEY=\"$API_KEY\""
    echo ""
    echo -e "${BLUE}CLI Usage:${NC}"
    echo ""
    echo "  # Set environment variables first (see above), then:"
    echo "  npx @e2b/cli template list"
    echo "  npx @e2b/cli sandbox list"
    echo ""
    echo "For more details, see E2B-LITE-DESIGN.md"
}

# =============================================================================
# Main execution
# =============================================================================

check_sudo

# Install dependencies if requested
if [[ "$INSTALL_DEPS" == "true" ]]; then
    install_dependencies
fi

# Exit if deps-only mode
if [[ "$DEPS_ONLY" == "true" ]]; then
    echo "Dependencies installed. Run again without --deps-only for full setup."
    exit 0
fi

# Run all setup steps
check_prerequisites
setup_kernel_modules
setup_hugepages
create_directories
download_artifacts

# Build or download binaries
if [[ "$USE_PREBUILT" == "true" ]]; then
    download_prebuilt_binaries
else
    build_binaries
fi

setup_npm_dependencies
start_infrastructure
run_migrations
seed_database
build_base_template
create_start_scripts
create_test_script

print_summary
