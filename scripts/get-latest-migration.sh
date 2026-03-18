#!/bin/bash
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

scope="${1:-core}"
case "$scope" in
  core)
    migration_dir="packages/db/migrations"
    ;;
  dashboard)
    migration_dir="packages/db/pkg/dashboard/migrations"
    ;;
  *)
    echo "unsupported scope: $scope" >&2
    exit 1
    ;;
esac

latest_version=$(git ls-tree --name-only HEAD -- "${migration_dir}/" | sed 's|.*/||' | sed 's/_.*//' | sort | tail -n 1)
echo "$latest_version"