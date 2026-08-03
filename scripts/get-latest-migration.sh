#!/bin/bash
set -euo pipefail

# Read the migrations from the filesystem, not from git: this script also
# runs where git metadata is absent or points at a different tree (docker
# build contexts, CI workspaces assembled from a subdirectory).
cd "$(dirname "$0")/.."

latest_version=$(ls packages/db/migrations/ | sed 's/_.*//' | sort | tail -n 1)
echo "$latest_version"
