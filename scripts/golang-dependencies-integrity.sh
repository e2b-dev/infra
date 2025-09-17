#!/bin/bash
set -euo pipefail

STRICT_MODE=${STRICT_MODE:-0}

# go mod tidy
modules=$(go work edit -json | jq -r '.Use[].DiskPath')
for dir in $modules; do
  echo "$dir"
  pushd "$dir" > /dev/null
  go mod tidy
  go run github.com/abhijit-hota/modfmt@afa6506f86937f6c3b76e4911d57b769fa4659a0 --in-place
  popd > /dev/null
done

# go sync
echo "Running 'go work sync' in the project root"
go work sync

# in strict mode check if go.mod or go.sum files have changed and optionally fail
if [[ "$STRICT_MODE" -eq 1 ]]; then
  if ! git diff --exit-code; then
    echo
    echo "❌ Unexpected changes in go.mod or go.sum files!"
    echo "Run 'make tidy' manually and commit the changes."
    exit 1
  fi
fi
