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

echo "Downloading busybox v${VERSION} (${ARCH}) from GCS..."

mkdir -p "$(dirname "$OUTPUT")"
curl -sfL -o "$OUTPUT" "${GCS_PREFIX}/busybox"
curl -sfL -o "${OUTPUT}.sha256" "${GCS_PREFIX}/busybox.sha256"

echo "Verifying SHA256..."
(cd "$(dirname "$OUTPUT")" && sha256sum -c "$(basename "$OUTPUT").sha256")

chmod +x "$OUTPUT"
echo "${VERSION}-${ARCH}" > "$STAMP"
rm -f "${OUTPUT}.sha256"
