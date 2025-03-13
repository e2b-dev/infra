#!/bin/bash
set -euo pipefail

STRICT_MODE=${STRICT_MODE:-0}

# go mod tidy
find packages tests -type f -name "go.sum" -exec dirname {} \; | sort -u | while read -r dir; do
  echo "Running 'go mod tidy' in $dir"
  (cd "$dir" && go mod tidy)
done

# go sync
echo "Running 'go work sync' in the project root"
go work sync

# in strict mode check if go.mod or go.sum files have changed and optionally fail
if [[ "$STRICT_MODE" -eq 1 ]]; then
  if ! git diff --exit-code -- '**/go.mod' '**/go.sum'; then
    echo
    echo "❌ Unexpected changes in go.mod or go.sum files!"
    echo "Run 'go mod tidy' and 'go work sync' manually and commit the changes."
    exit 1
  fi
fi
