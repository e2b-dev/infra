#!/usr/bin/env bash
# Syncs the envd version managed by changesets (packages/envd/package.json)
# into the Go version constant (packages/envd/pkg/version.go).
#
# Run from the repo root, after `pnpm changeset version`.
set -euo pipefail

cd "$(dirname "$0")/.."

package_json="packages/envd/package.json"
version_go="packages/envd/pkg/version.go"

version=$(jq -er .version "$package_json")

if ! [[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "❌ Unexpected version '$version' in $package_json" >&2
  exit 1
fi

if ! grep -qE 'const Version = "[0-9]+\.[0-9]+\.[0-9]+"' "$version_go"; then
  echo "❌ Could not find 'const Version = \"X.Y.Z\"' in $version_go" >&2
  exit 1
fi

sed -E -i.bak "s/const Version = \"[0-9]+\.[0-9]+\.[0-9]+\"/const Version = \"$version\"/" "$version_go"
rm "$version_go.bak"

echo "✅ Synced envd version $version into $version_go"
