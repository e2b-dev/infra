#!/bin/bash
set -euo pipefail

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
