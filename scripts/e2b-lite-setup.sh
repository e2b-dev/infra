#!/bin/bash
#
# E2B Lite Setup Script
#
# This script sets up a complete E2B Lite environment for local development.
# It handles prerequisites, downloads artifacts, builds binaries, starts
# infrastructure, and optionally builds the base template.
#
# Usage:
#   ./scripts/e2b-lite-setup.sh              # Full setup with clean progress UI
#   ./scripts/e2b-lite-setup.sh --verbose    # Full setup with detailed output
#   ./scripts/e2b-lite-setup.sh --check-req  # Only check requirements
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
DIM='\033[2m'
BOLD='\033[1m'
NC='\033[0m' # No Color

# Spinner characters
SPINNER_CHARS='⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏'
SPINNER_PID=""

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
VERBOSE=false
CHECK_REQ_ONLY=false

while [[ $# -gt 0 ]]; do
    case $1 in
        --verbose|-v)
            VERBOSE=true
            shift
            ;;
        --check-req)
            CHECK_REQ_ONLY=true
            shift
            ;;
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
            echo "  --verbose, -v      Show detailed output (apt, build logs, etc.)"
            echo "  --check-req        Only check if system meets requirements"
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

# -----------------------------------------------------------------------------
# Progress UI Functions
# -----------------------------------------------------------------------------

# Start spinner with message
start_spinner() {
    local msg="$1"
    if [[ "$VERBOSE" == "true" ]]; then
        echo -e "${BLUE}$msg${NC}"
        return
    fi

    # Print initial message
    printf "  %s " "$msg"

    # Start spinner in background (don't disown so wait works)
    (
        trap 'exit 0' TERM INT
        i=0
        while true; do
            printf "\r  %s ${SPINNER_CHARS:i++%${#SPINNER_CHARS}:1} " "$msg"
            sleep 0.1
        done
    ) &
    SPINNER_PID=$!
}

# Helper to kill spinner cleanly
kill_spinner() {
    if [[ -n "$SPINNER_PID" ]]; then
        kill $SPINNER_PID 2>/dev/null || true
        wait $SPINNER_PID 2>/dev/null || true
        SPINNER_PID=""
        # Clear the entire line
        printf "\r\033[2K"
    fi
}

# Stop spinner with success
stop_spinner_success() {
    local msg="${1:-}"
    kill_spinner
    if [[ "$VERBOSE" != "true" ]]; then
        if [[ -n "$msg" ]]; then
            printf "  ${GREEN}✓${NC} %s\n" "$msg"
        else
            printf "  ${GREEN}✓${NC}\n"
        fi
    fi
}

# Stop spinner with failure
stop_spinner_fail() {
    local msg="${1:-}"
    kill_spinner
    if [[ "$VERBOSE" != "true" ]]; then
        if [[ -n "$msg" ]]; then
            printf "  ${RED}✗${NC} %s\n" "$msg"
        else
            printf "  ${RED}✗${NC}\n"
        fi
    fi
}

# Stop spinner with warning
stop_spinner_warn() {
    local msg="${1:-}"
    kill_spinner
    if [[ "$VERBOSE" != "true" ]]; then
        if [[ -n "$msg" ]]; then
            printf "  ${YELLOW}!${NC} %s\n" "$msg"
        else
            printf "  ${YELLOW}!${NC}\n"
        fi
    fi
}

# Run command with optional output suppression
run_cmd() {
    local log_file="/tmp/e2b-setup-$$.log"
    if [[ "$VERBOSE" == "true" ]]; then
        "$@"
    else
        if "$@" >> "$log_file" 2>&1; then
            return 0
        else
            local exit_code=$?
            echo ""
            echo -e "${RED}Command failed. Last 20 lines of output:${NC}"
            tail -20 "$log_file" 2>/dev/null || true
            return $exit_code
        fi
    fi
}

# Print step header
print_step() {
    local step_num="$1"
    local total="$2"
    local msg="$3"
    echo ""
    if [[ "$VERBOSE" == "true" ]]; then
        echo -e "${BLUE}[$step_num/$total] $msg${NC}"
    else
        echo -e "${BOLD}[$step_num/$total]${NC} $msg"
    fi
}

# Print success line (for check results)
print_ok() {
    echo -e "  ${GREEN}✓${NC} $1"
}

# Print warning line
print_warn() {
    echo -e "  ${YELLOW}!${NC} $1"
}

# Print error line
print_err() {
    echo -e "  ${RED}✗${NC} $1"
}

# Cleanup on exit
cleanup_spinner() {
    if [[ -n "$SPINNER_PID" ]]; then
        kill $SPINNER_PID 2>/dev/null || true
        wait $SPINNER_PID 2>/dev/null || true
        printf "\r\033[2K"
    fi
}
trap cleanup_spinner EXIT

# Print banner
if [[ "$CHECK_REQ_ONLY" == "true" ]]; then
    echo ""
    echo -e "${BOLD}E2B Lite - Requirements Check${NC}"
    echo ""
else
    echo ""
    echo -e "${BOLD}E2B Lite Setup${NC}"
    if [[ "$VERBOSE" != "true" ]]; then
        echo -e "${DIM}Use --verbose for detailed output${NC}"
    fi
    echo ""
fi

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
    # Detect package manager
    if ! command -v apt-get &> /dev/null; then
        print_err "Only apt-based systems (Ubuntu/Debian) are currently supported"
        exit 1
    fi

    local installed_items=()

    if [[ "$VERBOSE" != "true" ]]; then
        start_spinner "Installing dependencies"
    fi

    # Update package list
    if [[ "$VERBOSE" == "true" ]]; then
        start_spinner "Updating package list"
    fi
    if run_cmd sudo apt-get update -qq; then
        if [[ "$VERBOSE" == "true" ]]; then
            stop_spinner_success "Package list updated"
        fi
    else
        stop_spinner_fail "Failed to update package list"
        exit 1
    fi

    # Install Docker if not present
    if ! command -v docker &> /dev/null; then
        if [[ "$VERBOSE" == "true" ]]; then
            start_spinner "Installing Docker"
        fi
        if run_cmd sudo install -m 0755 -d /etc/apt/keyrings && \
           run_cmd sudo curl -fsSL https://download.docker.com/linux/ubuntu/gpg -o /etc/apt/keyrings/docker.asc && \
           run_cmd sudo chmod a+r /etc/apt/keyrings/docker.asc; then

            echo "Types: deb
URIs: https://download.docker.com/linux/ubuntu
Suites: $(. /etc/os-release && echo "${UBUNTU_CODENAME:-$VERSION_CODENAME}")
Components: stable
Signed-By: /etc/apt/keyrings/docker.asc" | sudo tee /etc/apt/sources.list.d/docker.sources > /dev/null 2>&1

            if run_cmd sudo apt-get update -qq && \
               run_cmd sudo apt-get install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin; then
                if [[ "$VERBOSE" == "true" ]]; then
                    stop_spinner_success "Docker installed"
                fi
                installed_items+=("Docker")
            else
                stop_spinner_fail "Failed to install Docker"
                exit 1
            fi
        else
            stop_spinner_fail "Failed to setup Docker repository"
            exit 1
        fi
    fi

    # Install Go if not present
    if ! command -v go &> /dev/null; then
        if [[ "$VERBOSE" == "true" ]]; then
            start_spinner "Installing Go"
        fi
        if run_cmd sudo snap install --classic go; then
            if [[ "$VERBOSE" == "true" ]]; then
                stop_spinner_success "Go installed"
            fi
            installed_items+=("Go")
        else
            stop_spinner_fail "Failed to install Go"
            exit 1
        fi
    fi

    # Install Node.js if not present (needed for template building)
    if ! command -v node &> /dev/null; then
        if [[ "$VERBOSE" == "true" ]]; then
            start_spinner "Installing Node.js"
        fi
        if curl -fsSL https://deb.nodesource.com/setup_22.x 2>/dev/null | sudo -E bash - > /dev/null 2>&1 && \
           run_cmd sudo apt-get install -y nodejs; then
            if [[ "$VERBOSE" == "true" ]]; then
                stop_spinner_success "Node.js installed"
            fi
            installed_items+=("Node.js")
        else
            stop_spinner_fail "Failed to install Node.js"
            exit 1
        fi
    fi

    # Install build tools and other dependencies
    if [[ "$VERBOSE" == "true" ]]; then
        start_spinner "Installing build tools"
    fi
    if run_cmd sudo apt-get install -y build-essential make ca-certificates curl git net-tools; then
        if [[ "$VERBOSE" == "true" ]]; then
            stop_spinner_success "Build tools installed"
        fi
        installed_items+=("build-tools")
    else
        stop_spinner_fail "Failed to install build tools"
        exit 1
    fi

    # Summary for non-verbose mode
    if [[ "$VERBOSE" != "true" ]]; then
        if [[ ${#installed_items[@]} -gt 0 ]]; then
            stop_spinner_success "Installed: ${installed_items[*]}"
        else
            stop_spinner_success "All dependencies already installed"
        fi
    fi
}

# -----------------------------------------------------------------------------
# Check prerequisites
# -----------------------------------------------------------------------------
check_prerequisites() {
    local has_errors=false
    local has_warnings=false
    local warning_msg=""

    # Check OS
    if [[ "$OSTYPE" != "linux-gnu"* ]]; then
        if [[ "$VERBOSE" == "true" ]] || [[ "$CHECK_REQ_ONLY" == "true" ]]; then
            print_err "E2B Lite requires Linux. Detected: $OSTYPE"
        fi
        has_errors=true
    else
        if [[ "$VERBOSE" == "true" ]] || [[ "$CHECK_REQ_ONLY" == "true" ]]; then
            print_ok "Linux detected"
        fi
    fi

    # Check kernel version
    KERNEL_MAJOR=$(uname -r | cut -d. -f1)
    KERNEL_MINOR=$(uname -r | cut -d. -f2)
    if [ "$KERNEL_MAJOR" -lt 5 ] || ([ "$KERNEL_MAJOR" -eq 5 ] && [ "$KERNEL_MINOR" -lt 10 ]); then
        if [[ "$VERBOSE" == "true" ]] || [[ "$CHECK_REQ_ONLY" == "true" ]]; then
            print_err "Kernel $(uname -r) is too old. Minimum required: 5.10"
        fi
        has_errors=true
    else
        if [[ "$VERBOSE" == "true" ]] || [[ "$CHECK_REQ_ONLY" == "true" ]]; then
            print_ok "Kernel $(uname -r)"
        fi
    fi

    # Check for kernel 6.8+ (needed for building templates)
    if [ "$KERNEL_MAJOR" -lt 6 ] || ([ "$KERNEL_MAJOR" -eq 6 ] && [ "$KERNEL_MINOR" -lt 8 ]); then
        if [[ "$VERBOSE" == "true" ]] || [[ "$CHECK_REQ_ONLY" == "true" ]]; then
            print_warn "Kernel < 6.8: You can run sandboxes but cannot build custom templates"
        fi
        BUILD_TEMPLATE=false
        has_warnings=true
        warning_msg="kernel < 6.8"
    else
        if [[ "$VERBOSE" == "true" ]] || [[ "$CHECK_REQ_ONLY" == "true" ]]; then
            print_ok "Kernel 6.8+: Full support (running + building templates)"
        fi
    fi

    # Check KVM
    if [[ ! -e /dev/kvm ]]; then
        if [[ "$VERBOSE" == "true" ]] || [[ "$CHECK_REQ_ONLY" == "true" ]]; then
            print_err "/dev/kvm not found. KVM is required"
            echo "       Enable KVM: sudo modprobe kvm_intel (or kvm_amd)"
        fi
        has_errors=true
    elif [[ ! -r /dev/kvm ]] || [[ ! -w /dev/kvm ]]; then
        if [[ "$VERBOSE" == "true" ]] || [[ "$CHECK_REQ_ONLY" == "true" ]]; then
            print_warn "No read/write access to /dev/kvm"
            echo "       Fix: sudo usermod -aG kvm \$USER && newgrp kvm"
        fi
        has_warnings=true
    else
        if [[ "$VERBOSE" == "true" ]] || [[ "$CHECK_REQ_ONLY" == "true" ]]; then
            print_ok "KVM available"
        fi
    fi

    # Check Docker
    if ! command -v docker &> /dev/null; then
        if [[ "$VERBOSE" == "true" ]] || [[ "$CHECK_REQ_ONLY" == "true" ]]; then
            if [[ "$CHECK_REQ_ONLY" == "true" ]]; then
                print_err "Docker not found"
            else
                print_err "Docker not found. Run with --deps-only first or install manually"
            fi
        fi
        has_errors=true
    elif ! docker info &> /dev/null 2>&1; then
        if [[ "$VERBOSE" == "true" ]] || [[ "$CHECK_REQ_ONLY" == "true" ]]; then
            print_err "Docker daemon not running or no permission"
            echo "       Start Docker: sudo systemctl start docker"
            echo "       Or add to group: sudo usermod -aG docker \$USER && newgrp docker"
        fi
        has_errors=true
    else
        if [[ "$VERBOSE" == "true" ]] || [[ "$CHECK_REQ_ONLY" == "true" ]]; then
            print_ok "Docker available"
        fi
    fi

    # Check Go
    if ! command -v go &> /dev/null; then
        if [[ "$VERBOSE" == "true" ]] || [[ "$CHECK_REQ_ONLY" == "true" ]]; then
            if [[ "$CHECK_REQ_ONLY" == "true" ]]; then
                print_err "Go not found"
            else
                print_err "Go not found. Run with --deps-only first or install manually"
            fi
        fi
        has_errors=true
    else
        GO_VERSION=$(go version | grep -oP '\d+\.\d+' | head -1)
        if [[ "$VERBOSE" == "true" ]] || [[ "$CHECK_REQ_ONLY" == "true" ]]; then
            print_ok "Go $GO_VERSION"
        fi
    fi

    # Check Node.js (optional, for template building)
    if command -v node &> /dev/null; then
        if [[ "$VERBOSE" == "true" ]] || [[ "$CHECK_REQ_ONLY" == "true" ]]; then
            print_ok "Node.js $(node --version)"
        fi
    else
        if [[ "$VERBOSE" == "true" ]] || [[ "$CHECK_REQ_ONLY" == "true" ]]; then
            print_warn "Node.js not found (needed for template building)"
        fi
        has_warnings=true
    fi

    # Return appropriate exit code for check-req mode
    if [[ "$CHECK_REQ_ONLY" == "true" ]]; then
        echo ""
        if [[ "$has_errors" == "true" ]]; then
            echo -e "${RED}Some requirements are not met.${NC}"
            echo "Install missing dependencies with: ./scripts/e2b-lite-setup.sh --deps-only"
            exit 1
        elif [[ "$has_warnings" == "true" ]]; then
            echo -e "${YELLOW}System is ready with some limitations.${NC}"
            exit 0
        else
            echo -e "${GREEN}All requirements met. System is ready for E2B Lite.${NC}"
            exit 0
        fi
    fi

    # Summary for non-verbose mode
    if [[ "$VERBOSE" != "true" ]]; then
        if [[ "$has_errors" == "true" ]]; then
            print_err "Prerequisites check failed"
            exit 1
        elif [[ "$has_warnings" == "true" ]]; then
            print_warn "System ready ($warning_msg)"
        else
            print_ok "All prerequisites met"
        fi
    fi

    # For non-check-req mode, exit on errors
    if [[ "$has_errors" == "true" ]]; then
        exit 1
    fi
}

# -----------------------------------------------------------------------------
# Setup system (kernel modules, hugepages, directories)
# -----------------------------------------------------------------------------
setup_system() {
    local config_items=()

    if [[ "$VERBOSE" != "true" ]]; then
        start_spinner "Configuring system"
    fi

    # NBD module with sufficient devices
    if ! lsmod | grep -q "^nbd "; then
        if [[ "$VERBOSE" == "true" ]]; then
            start_spinner "Loading NBD module"
        fi
        if sudo modprobe nbd nbds_max=128 2>/dev/null; then
            if [[ "$VERBOSE" == "true" ]]; then
                stop_spinner_success "NBD module loaded (nbds_max=128)"
            fi
            config_items+=("NBD")
        else
            if [[ "$VERBOSE" == "true" ]]; then
                stop_spinner_warn "Failed to load NBD module (may need to install)"
            fi
        fi
    else
        # Check if we have enough NBD devices
        NBD_COUNT=$(ls -1 /dev/nbd* 2>/dev/null | wc -l)
        if [ "$NBD_COUNT" -lt 64 ]; then
            if [[ "$VERBOSE" == "true" ]]; then
                start_spinner "Reloading NBD module with more devices"
            fi
            sudo rmmod nbd 2>/dev/null || true
            sudo modprobe nbd nbds_max=128
            if [[ "$VERBOSE" == "true" ]]; then
                stop_spinner_success "NBD module reloaded"
            fi
            config_items+=("NBD")
        fi
    fi

    # TUN module
    if ! lsmod | grep -q "^tun "; then
        if [[ "$VERBOSE" == "true" ]]; then
            start_spinner "Loading TUN module"
        fi
        if sudo modprobe tun 2>/dev/null; then
            if [[ "$VERBOSE" == "true" ]]; then
                stop_spinner_success "TUN module loaded"
            fi
            config_items+=("TUN")
        else
            if [[ "$VERBOSE" == "true" ]]; then
                stop_spinner_warn "Failed to load TUN module"
            fi
        fi
    fi

    # HugePages
    HUGEPAGES_TOTAL=$(cat /proc/sys/vm/nr_hugepages 2>/dev/null || echo 0)
    HUGEPAGES_NEEDED=2048  # 2048 * 2MB = 4GB reserved for HugePages

    if [ "$HUGEPAGES_TOTAL" -lt "$HUGEPAGES_NEEDED" ]; then
        if [[ "$VERBOSE" == "true" ]]; then
            start_spinner "Allocating HugePages (4GB)"
        fi
        if echo "$HUGEPAGES_NEEDED" | sudo tee /proc/sys/vm/nr_hugepages > /dev/null 2>&1; then
            if ! grep -q "vm.nr_hugepages" /etc/sysctl.conf 2>/dev/null; then
                echo "vm.nr_hugepages=$HUGEPAGES_NEEDED" | sudo tee -a /etc/sysctl.conf > /dev/null
            else
                sudo sed -i "s/vm.nr_hugepages=.*/vm.nr_hugepages=$HUGEPAGES_NEEDED/" /etc/sysctl.conf
            fi
            if [[ "$VERBOSE" == "true" ]]; then
                stop_spinner_success "HugePages configured (persistent)"
            fi
            config_items+=("HugePages")
        else
            if [[ "$VERBOSE" == "true" ]]; then
                stop_spinner_warn "Failed to allocate HugePages"
                echo "       Manual fix: echo $HUGEPAGES_NEEDED | sudo tee /proc/sys/vm/nr_hugepages"
            fi
        fi
    fi

    # Create directories
    if [[ "$VERBOSE" == "true" ]]; then
        start_spinner "Creating directory structure"
    fi

    mkdir -p "$FC_VERSIONS_DIR/$FC_VERSION"
    mkdir -p "$KERNELS_DIR/$KERNEL_VERSION"
    mkdir -p "$TMP_DIR/templates"
    mkdir -p "$TMP_DIR/orchestrator"
    mkdir -p "$TMP_DIR/sandbox"
    mkdir -p "$TMP_DIR/sandbox-cache"
    mkdir -p "$TMP_DIR/snapshot-cache"
    mkdir -p "$ORCHESTRATOR_DIR/tmp/local-template-storage"
    mkdir -p "$ORCHESTRATOR_DIR/tmp/sandbox"
    mkdir -p "$ORCHESTRATOR_DIR/tmp/snapshot-cache"
    mkdir -p "$ORCHESTRATOR_DIR/tmp/orchestrator/sandbox"
    mkdir -p "$ORCHESTRATOR_DIR/tmp/orchestrator/template"
    mkdir -p "$ORCHESTRATOR_DIR/tmp/orchestrator/build"
    mkdir -p "$ORCHESTRATOR_DIR/tmp/orchestrator/build-templates"

    if [[ "$VERBOSE" == "true" ]]; then
        stop_spinner_success "Directories created"
    fi
    config_items+=("directories")

    # Summary for non-verbose mode
    if [[ "$VERBOSE" != "true" ]]; then
        stop_spinner_success "System configured (${config_items[*]})"
    fi
}

# -----------------------------------------------------------------------------
# Download artifacts
# Note: When using create-build with -storage flag, kernel and firecracker
# are downloaded automatically to $storage/kernels and $storage/fc-versions.
# This function is kept for backwards compatibility but can be skipped.
# -----------------------------------------------------------------------------
download_artifacts() {
    if [[ "$VERBOSE" == "true" ]]; then
        echo "Note: create-build tool will download kernel and firecracker automatically"
    fi
}

# -----------------------------------------------------------------------------
# Download pre-built binaries
# -----------------------------------------------------------------------------
download_prebuilt_binaries() {
    GITHUB_REPO="e2b-dev/infra"
    BUILD_API=false
    BUILD_ORCH=false
    BUILD_PROXY=false
    BUILD_ENVD=false

    # Determine version to download
    if [[ "$PREBUILT_VERSION" == "latest" ]]; then
        start_spinner "Fetching latest release info"
        RELEASE_URL="https://api.github.com/repos/$GITHUB_REPO/releases/latest"
        RELEASE_INFO=$(curl -fsSL "$RELEASE_URL" 2>/dev/null)
        if [[ -z "$RELEASE_INFO" ]]; then
            stop_spinner_warn "Failed to fetch release info, building from source"
            build_binaries
            return
        fi
        VERSION=$(echo "$RELEASE_INFO" | grep -oP '"tag_name":\s*"\K[^"]+' | head -1)
        if [[ -z "$VERSION" ]]; then
            stop_spinner_warn "No releases found, building from source"
            build_binaries
            return
        fi
        stop_spinner_success "Found version $VERSION"
    else
        VERSION="$PREBUILT_VERSION"
    fi

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
        print_ok "API already exists"
    else
        start_spinner "Downloading API"
        if curl -fsSL "$BASE_URL/api-linux-amd64" -o "$API_PATH" 2>/dev/null; then
            chmod +x "$API_PATH"
            stop_spinner_success "API downloaded"
        else
            stop_spinner_warn "Failed to download API"
            BUILD_API=true
        fi
    fi

    # Download Orchestrator
    ORCH_PATH="$ORCHESTRATOR_DIR/bin/orchestrator"
    if [[ -f "$ORCH_PATH" ]]; then
        print_ok "Orchestrator already exists"
    else
        start_spinner "Downloading Orchestrator"
        if curl -fsSL "$BASE_URL/orchestrator-linux-amd64" -o "$ORCH_PATH" 2>/dev/null; then
            chmod +x "$ORCH_PATH"
            stop_spinner_success "Orchestrator downloaded"
        else
            stop_spinner_warn "Failed to download Orchestrator"
            BUILD_ORCH=true
        fi
    fi

    # Download Client-Proxy
    PROXY_PATH="$CLIENT_PROXY_DIR/bin/client-proxy"
    if [[ -f "$PROXY_PATH" ]]; then
        print_ok "Client-Proxy already exists"
    else
        start_spinner "Downloading Client-Proxy"
        if curl -fsSL "$BASE_URL/client-proxy-linux-amd64" -o "$PROXY_PATH" 2>/dev/null; then
            chmod +x "$PROXY_PATH"
            stop_spinner_success "Client-Proxy downloaded"
        else
            stop_spinner_warn "Failed to download Client-Proxy"
            BUILD_PROXY=true
        fi
    fi

    # Download Envd
    ENVD_PATH="$ENVD_DIR/bin/envd"
    if [[ -f "$ENVD_PATH" ]]; then
        print_ok "envd already exists"
    else
        start_spinner "Downloading envd"
        if curl -fsSL "$BASE_URL/envd-linux-amd64" -o "$ENVD_PATH" 2>/dev/null; then
            chmod +x "$ENVD_PATH"
            stop_spinner_success "envd downloaded"
        else
            stop_spinner_warn "Failed to download envd"
            BUILD_ENVD=true
        fi
    fi

    # Build any that failed to download
    if [[ "$BUILD_API" == "true" ]] || [[ "$BUILD_ORCH" == "true" ]] || \
       [[ "$BUILD_PROXY" == "true" ]] || [[ "$BUILD_ENVD" == "true" ]]; then

        if [[ "$BUILD_ENVD" == "true" ]]; then
            start_spinner "Building envd from source"
            if run_cmd make -C "$ENVD_DIR" build; then
                stop_spinner_success "envd built"
            else
                stop_spinner_fail "Failed to build envd"
            fi
        fi

        if [[ "$BUILD_API" == "true" ]]; then
            start_spinner "Building API from source"
            if run_cmd make -C "$API_DIR" build; then
                stop_spinner_success "API built"
            else
                stop_spinner_fail "Failed to build API"
            fi
        fi

        if [[ "$BUILD_ORCH" == "true" ]]; then
            start_spinner "Building Orchestrator from source"
            if run_cmd make -C "$ORCHESTRATOR_DIR" build-debug; then
                stop_spinner_success "Orchestrator built"
            else
                stop_spinner_fail "Failed to build Orchestrator"
            fi
        fi

        if [[ "$BUILD_PROXY" == "true" ]]; then
            start_spinner "Building Client-Proxy from source"
            if run_cmd make -C "$CLIENT_PROXY_DIR" build; then
                stop_spinner_success "Client-Proxy built"
            else
                stop_spinner_fail "Failed to build Client-Proxy"
            fi
        fi
    fi
}

# -----------------------------------------------------------------------------
# Build all binaries
# -----------------------------------------------------------------------------
build_binaries() {
    local built_items=()
    local existing_items=()

    if [[ "$VERBOSE" != "true" ]]; then
        start_spinner "Building binaries"
    fi

    # Build envd - MUST use regular build (not build-debug) for static linking
    ENVD_PATH="$ENVD_DIR/bin/envd"
    if [[ -f "$ENVD_PATH" ]]; then
        existing_items+=("envd")
    else
        if [[ "$VERBOSE" == "true" ]]; then
            start_spinner "Building envd"
        fi
        if run_cmd make -C "$ENVD_DIR" build; then
            if [[ "$VERBOSE" == "true" ]]; then
                stop_spinner_success "envd built"
            fi
            built_items+=("envd")
        else
            stop_spinner_fail "Failed to build envd"
            exit 1
        fi
    fi

    # Build API
    API_PATH="$API_DIR/bin/api"
    if [[ -f "$API_PATH" ]]; then
        existing_items+=("API")
    else
        if [[ "$VERBOSE" == "true" ]]; then
            start_spinner "Building API"
        fi
        if run_cmd make -C "$API_DIR" build; then
            if [[ "$VERBOSE" == "true" ]]; then
                stop_spinner_success "API built"
            fi
            built_items+=("API")
        else
            stop_spinner_fail "Failed to build API"
            exit 1
        fi
    fi

    # Build Orchestrator
    ORCH_PATH="$ORCHESTRATOR_DIR/bin/orchestrator"
    if [[ -f "$ORCH_PATH" ]]; then
        existing_items+=("Orchestrator")
    else
        if [[ "$VERBOSE" == "true" ]]; then
            start_spinner "Building Orchestrator"
        fi
        if run_cmd make -C "$ORCHESTRATOR_DIR" build-debug; then
            if [[ "$VERBOSE" == "true" ]]; then
                stop_spinner_success "Orchestrator built"
            fi
            built_items+=("Orchestrator")
        else
            stop_spinner_fail "Failed to build Orchestrator"
            exit 1
        fi
    fi

    # Build Client-Proxy
    PROXY_PATH="$CLIENT_PROXY_DIR/bin/client-proxy"
    if [[ -f "$PROXY_PATH" ]]; then
        existing_items+=("Client-Proxy")
    else
        if [[ "$VERBOSE" == "true" ]]; then
            start_spinner "Building Client-Proxy"
        fi
        if run_cmd make -C "$CLIENT_PROXY_DIR" build; then
            if [[ "$VERBOSE" == "true" ]]; then
                stop_spinner_success "Client-Proxy built"
            fi
            built_items+=("Client-Proxy")
        else
            stop_spinner_fail "Failed to build Client-Proxy"
            exit 1
        fi
    fi

    # Summary for non-verbose mode
    if [[ "$VERBOSE" != "true" ]]; then
        if [[ ${#built_items[@]} -gt 0 ]]; then
            stop_spinner_success "Built: ${built_items[*]}"
        else
            stop_spinner_success "All binaries already built"
        fi
    fi
}

# -----------------------------------------------------------------------------
# Setup npm dependencies for template building
# -----------------------------------------------------------------------------
setup_npm_dependencies() {
    if ! command -v npm &> /dev/null; then
        if [[ "$VERBOSE" == "true" ]]; then
            print_warn "Skipping npm dependencies (npm not found)"
        fi
        return
    fi

    if [[ -d "$SHARED_SCRIPTS_DIR" ]]; then
        if [[ ! -d "$SHARED_SCRIPTS_DIR/node_modules" ]]; then
            start_spinner "Installing npm packages"
            if (cd "$SHARED_SCRIPTS_DIR" && run_cmd npm install --silent); then
                stop_spinner_success "npm dependencies installed"
            else
                stop_spinner_warn "Failed to install npm packages"
            fi
        else
            print_ok "npm dependencies ready"
        fi
    fi
}

# -----------------------------------------------------------------------------
# Start Docker infrastructure
# -----------------------------------------------------------------------------
start_infrastructure() {
    COMPOSE_FILE="$LOCAL_DEV_DIR/docker-compose.yaml"

    if [[ ! -f "$COMPOSE_FILE" ]]; then
        print_err "docker-compose.yaml not found at $COMPOSE_FILE"
        exit 1
    fi

    if docker ps --format '{{.Names}}' | grep -q "local-dev-postgres"; then
        print_ok "Infrastructure already running"
    else
        start_spinner "Starting Docker infrastructure"
        if run_cmd docker compose -f "$COMPOSE_FILE" up -d; then
            for i in {1..30}; do
                if docker exec local-dev-postgres-1 pg_isready -U postgres > /dev/null 2>&1; then
                    break
                fi
                sleep 1
            done
            stop_spinner_success "Docker infrastructure started"
        else
            stop_spinner_fail "Failed to start infrastructure"
            exit 1
        fi
    fi
}

# -----------------------------------------------------------------------------
# Configure database (migrations + seeding)
# -----------------------------------------------------------------------------
configure_database() {
    export POSTGRES_CONNECTION_STRING="postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable"

    local db_actions=()

    if [[ "$VERBOSE" != "true" ]]; then
        start_spinner "Configuring database"
    fi

    # Run migrations
    if [[ "$VERBOSE" == "true" ]]; then
        start_spinner "Running database migrations"
    fi
    if run_cmd make -C "$REPO_ROOT/packages/db" migrate-local; then
        if [[ "$VERBOSE" == "true" ]]; then
            stop_spinner_success "Migrations applied"
        fi
        db_actions+=("migrations")
    else
        if [[ "$VERBOSE" == "true" ]]; then
            stop_spinner_warn "Migrations may have failed or already applied"
        fi
    fi

    # Check if already seeded
    TEAM_EXISTS=$(docker exec local-dev-postgres-1 psql -U postgres -tAc "SELECT COUNT(*) FROM teams WHERE id='0b8a3ded-4489-4722-afd1-1d82e64ec2d5';" 2>/dev/null || echo "0")

    if [[ "$TEAM_EXISTS" == "1" ]]; then
        if [[ "$VERBOSE" == "true" ]]; then
            print_ok "Database already seeded"
        fi
    else
        if [[ "$VERBOSE" == "true" ]]; then
            start_spinner "Seeding database"
        fi
        if (cd "$LOCAL_DEV_DIR" && run_cmd go run seed-local-database.go); then
            if [[ "$VERBOSE" == "true" ]]; then
                stop_spinner_success "Database seeded"
            fi
            db_actions+=("seeded")
        else
            if [[ "$VERBOSE" == "true" ]]; then
                stop_spinner_warn "Seeding may have failed"
            fi
        fi
    fi

    # Summary for non-verbose mode
    if [[ "$VERBOSE" != "true" ]]; then
        if [[ ${#db_actions[@]} -gt 0 ]]; then
            stop_spinner_success "Database configured (${db_actions[*]})"
        else
            stop_spinner_success "Database already configured"
        fi
    fi
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
        print_warn "Skipping template build (--no-template or kernel < 6.8)"
        return
    fi

    # Check if template already exists in database
    EXISTING_TEMPLATE=$(docker exec local-dev-postgres-1 psql -U postgres -tAc "SELECT id FROM envs LIMIT 1;" 2>/dev/null | tr -d ' ' || echo "")
    if [[ -n "$EXISTING_TEMPLATE" ]]; then
        print_ok "Template already exists: $EXISTING_TEMPLATE"
        return
    fi

    # Check if template files exist but aren't registered
    TEMPLATE_STORAGE="$ORCHESTRATOR_DIR/tmp/local-template-storage"
    EXISTING_BUILD=$(ls -1 "$TEMPLATE_STORAGE" 2>/dev/null | head -1)

    if [[ -n "$EXISTING_BUILD" ]]; then
        start_spinner "Registering existing template"
        TEMPLATE_ID=$(generate_template_id)
        BUILD_ID="$EXISTING_BUILD"
        register_template "$TEMPLATE_ID" "$BUILD_ID"
        stop_spinner_success "Template registered: $TEMPLATE_ID"
        return
    fi

    # Set environment for template building
    export HOST_ENVD_PATH="$ENVD_DIR/bin/envd"
    export LOCAL_TEMPLATE_STORAGE_BASE_PATH="$TEMPLATE_STORAGE"

    # Generate IDs
    TEMPLATE_ID=$(generate_template_id)
    BUILD_ID=$(cat /proc/sys/kernel/random/uuid)

    start_spinner "Building base template (this may take a few minutes)"

    if [[ "$VERBOSE" == "true" ]]; then
        echo ""
        echo "  Template ID: $TEMPLATE_ID"
        echo "  Build ID: $BUILD_ID"
    fi

    if go run "$ORCHESTRATOR_DIR/cmd/create-build/main.go" \
        -template "$TEMPLATE_ID" \
        -to-build "$BUILD_ID" \
        -storage "$ORCHESTRATOR_DIR/tmp" \
        -kernel "$KERNEL_VERSION" \
        -firecracker "$FC_VERSION" \
        -vcpu 2 \
        -memory 512 \
        -disk 1024 \
        -v > /tmp/template-build.log 2>&1; then
        stop_spinner_success "Template built: $TEMPLATE_ID"

        # Register template in database
        register_template "$TEMPLATE_ID" "$BUILD_ID"
        print_ok "Template registered in database"
    else
        stop_spinner_warn "Template build failed"
        echo "       Check /tmp/template-build.log for details"
        echo "       You can build it manually later with:"
        echo "       make -C packages/shared/scripts local-build-base-template"
    fi
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
        VALUES ('$BUILD_ID', '$TEMPLATE_ID', 'uploaded', 2, 512, 1024, 1024, '$KERNEL_VERSION', '$FC_VERSION', '0.2.0', 'local', NOW(), NOW(), NOW())
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
    start_spinner "Creating service scripts"

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
FIRECRACKER_VERSIONS_DIR=./tmp/fc-versions \
HOST_ENVD_PATH=../envd/bin/envd \
HOST_KERNELS_DIR=./tmp/kernels \
LOCAL_TEMPLATE_STORAGE_BASE_PATH=./tmp/local-template-storage \
LOGS_COLLECTOR_ADDRESS=http://localhost:30006 \
ORCHESTRATOR_BASE_PATH=./tmp/orchestrator \
ORCHESTRATOR_LOCK_PATH=./tmp/.lock \
ORCHESTRATOR_SERVICES=orchestrator,template-manager \
OTEL_COLLECTOR_GRPC_ENDPOINT=localhost:4317 \
REDIS_URL=localhost:6379 \
SANDBOX_CACHE_DIR=./tmp/orchestrator/sandbox \
SANDBOX_DIR="${ORCH_DIR}/tmp/sandbox" \
SNAPSHOT_CACHE_DIR=./tmp/snapshot-cache \
TEMPLATE_CACHE_DIR=./tmp/orchestrator/template \
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
#   ./scripts/services/start-all.sh           # Start in background (default)
#   ./scripts/services/start-all.sh --fg      # Start in foreground (Ctrl+C to stop)
#

cd "$(dirname "$0")/../.." || exit 1
REPO_ROOT="$(pwd)"

FOREGROUND=false
if [[ "$1" == "--fg" ]]; then
    FOREGROUND=true
fi

echo "Starting E2B Lite services..."

if [[ "$FOREGROUND" == "true" ]]; then
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
else
    # Background mode (default)
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
fi
SCRIPT
    chmod +x "$REPO_ROOT/scripts/services/start-all.sh"

    stop_spinner_success "Service scripts created"
}

# -----------------------------------------------------------------------------
# Create test script
# -----------------------------------------------------------------------------
create_test_script() {
    start_spinner "Creating test script"

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

    stop_spinner_success "Test script created"
}

# -----------------------------------------------------------------------------
# Summary
# -----------------------------------------------------------------------------
print_summary() {
    # Get template ID from database
    TEMPLATE_ID=$(docker exec local-dev-postgres-1 psql -U postgres -tAc "SELECT id FROM envs LIMIT 1;" 2>/dev/null | tr -d ' ' || echo "")

    echo ""
    echo -e "${GREEN}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
    echo -e "${GREEN}  E2B Lite Setup Complete!${NC}"
    echo -e "${GREEN}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
    echo ""
    echo -e "${BOLD}Next Steps:${NC}"
    echo ""
    echo "  1. Start all services:"
    echo "     ./scripts/services/start-all.sh"
    echo ""
    echo "  2. Test with Python SDK:"
    echo "     python3 -m venv e2b_lite_venv"
    echo "     source e2b_lite_venv/bin/activate"
    echo "     pip install e2b && python scripts/test-e2b-lite.py"
    echo ""
    echo -e "${BOLD}Environment Variables (copy & paste):${NC}"
    echo ""
    echo "export E2B_API_KEY=\"$API_KEY\""
    echo "export E2B_API_URL=\"http://localhost:80\""
    echo "export E2B_SANDBOX_URL=\"http://localhost:3002\""
    echo "export E2B_ACCESS_TOKEN=\"$ACCESS_TOKEN\""
    if [[ -n "$TEMPLATE_ID" ]]; then
        echo "export E2B_TEMPLATE_ID=\"$TEMPLATE_ID\""
    fi
    echo ""
    echo -e "${BOLD}CLI Usage:${NC}"
    echo ""
    echo "  npx @e2b/cli template list"
    echo "  npx @e2b/cli sandbox list"
    echo ""
    echo -e "${DIM}For detailed usage, see E2B-LITE-DESIGN.md${NC}"
    echo ""
}

# =============================================================================
# Main execution
# =============================================================================

# Count total steps for progress display
TOTAL_STEPS=8
CURRENT_STEP=0

next_step() {
    CURRENT_STEP=$((CURRENT_STEP + 1))
    print_step "$CURRENT_STEP" "$TOTAL_STEPS" "$1"
}

# Handle --check-req mode
if [[ "$CHECK_REQ_ONLY" == "true" ]]; then
    check_prerequisites
    exit 0
fi

check_sudo

# Install dependencies if requested
if [[ "$INSTALL_DEPS" == "true" ]]; then
    next_step "Installing dependencies"
    install_dependencies
fi

# Exit if deps-only mode
if [[ "$DEPS_ONLY" == "true" ]]; then
    echo ""
    echo -e "${GREEN}Dependencies installed.${NC}"
    echo "Run again without --deps-only for full setup."
    exit 0
fi

# Run all setup steps
next_step "Checking prerequisites"
check_prerequisites

next_step "Configuring system"
setup_system
download_artifacts

next_step "Building binaries"
if [[ "$USE_PREBUILT" == "true" ]]; then
    download_prebuilt_binaries
else
    build_binaries
fi

next_step "Starting infrastructure"
start_infrastructure
setup_npm_dependencies

next_step "Configuring database"
configure_database

next_step "Building template"
build_base_template

next_step "Creating scripts"
create_start_scripts
create_test_script

print_summary
