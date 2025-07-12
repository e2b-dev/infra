#!/bin/bash
set -e

echo "Building E2B services with Docker Compose..."

# Function to prepare build context
prepare_build_context() {
    local service_dir=$1
    shift
    local additional_dirs=("$@")
    
    echo "Preparing build context for $service_dir..."
    
    # Copy additional directories to the service directory
    for dir in "${additional_dirs[@]}"; do
        local src_dir="packages/${dir#.}"
        local dest_name="${dir}"
        
        if [ -d "$src_dir" ]; then
            echo "  Copying $src_dir to $service_dir/$dest_name"
            rm -rf "$service_dir/$dest_name"
            cp -r "$src_dir" "$service_dir/$dest_name"
        fi
    done
}

# Clean up function
cleanup() {
    echo "Cleaning up temporary build files..."
    find packages -name ".shared" -type d -exec rm -rf {} + 2>/dev/null || true
    find packages -name ".db" -type d -exec rm -rf {} + 2>/dev/null || true
    find packages -name ".clickhouse" -type d -exec rm -rf {} + 2>/dev/null || true
}

# Set trap to cleanup on exit
trap cleanup EXIT

# Prepare build contexts
prepare_build_context "packages/api" ".shared" ".db" ".clickhouse"
prepare_build_context "packages/client-proxy" ".shared"
prepare_build_context "packages/docker-reverse-proxy" ".shared"
prepare_build_context "packages/orchestrator" ".shared"
prepare_build_context "packages/db" ".shared"

# Build with docker-compose
if [ "$1" == "minimal" ]; then
    echo "Building minimal setup..."
    docker-compose -f docker-compose.minimal.yml build "${@:2}"
else
    echo "Building full setup..."
    docker-compose build "$@"
fi

echo "Build complete!" 