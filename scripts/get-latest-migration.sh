#!/bin/bash
set -euo pipefail

latest_version=$(git ls-tree --name-only HEAD ../db/migrations/* | sed 's|../db/migrations/||' | sed 's/_.*//' | sort | tail -n 1)
echo "$latest_version"