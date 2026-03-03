#!/bin/bash
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

latest_version=$(git ls-tree --name-only HEAD -- packages/db/migrations/ | sed 's|.*/||' | sed 's/_.*//' | sort | tail -n 1)
echo "$latest_version"