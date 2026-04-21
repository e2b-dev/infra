#!/usr/bin/env bash
#
# Download busybox from GCS public builds bucket and verify SHA256.
# Skips download if binary exists and version/arch match the stamp file.
#
# Usage:
#   ./scripts/fetch-busybox.sh <version> <arch> <output_path>
#
# Example:
#   ./scripts/fetch-busybox.sh 1.36.1 amd64 .busybox/1.36.1/amd64/busybox

set -euo pipefail

VERSION="${1:?Usage: fetch-busybox.sh <version> <arch> <output_path>}"
ARCH="${2:?Usage: fetch-busybox.sh <version> <arch> <output_path>}"
OUTPUT="${3:?Usage: fetch-busybox.sh <version> <arch> <output_path>}"
STAMP="${OUTPUT}.stamp"

# Skip if binary exists and version/arch match
if [ -f "$OUTPUT" ] && [ "$(cat "$STAMP" 2>/dev/null)" = "${VERSION}-${ARCH}" ]; then
  exit 0
fi

GCS_PREFIX="https://storage.googleapis.com/e2b-prod-public-builds/busybox/${VERSION}/${ARCH}"
TMP_BIN=$(mktemp)
TMP_SHA=$(mktemp)

cleanup() { rm -f "$TMP_BIN" "$TMP_SHA"; }
trap cleanup EXIT

echo "Downloading busybox v${VERSION} (${ARCH}) from GCS..."

curl -sfL -o "$TMP_BIN" "${GCS_PREFIX}/busybox"
curl -sfL -o "$TMP_SHA" "${GCS_PREFIX}/busybox.sha256"

echo "Verifying SHA256..."
(cd "$(dirname "$TMP_BIN")" && sed "s|busybox|$(basename "$TMP_BIN")|" "$TMP_SHA" | sha256sum -c -)

mkdir -p "$(dirname "$OUTPUT")"
mv "$TMP_BIN" "$OUTPUT"
chmod +x "$OUTPUT"
echo "${VERSION}-${ARCH}" > "$STAMP"
