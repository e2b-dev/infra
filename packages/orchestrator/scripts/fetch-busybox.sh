#!/usr/bin/env bash
#
# Download busybox from GCS public builds bucket.
# Skips download if binary exists and version/arch match the stamp file.
#
# Usage:
#   ./scripts/fetch-busybox.sh <version> <arch> <output_path>
#
# Example:
#   ./scripts/fetch-busybox.sh 1.36.1 amd64 .busybox/amd64/busybox

set -euo pipefail

VERSION="${1:?Usage: fetch-busybox.sh <version> <arch> <output_path>}"
ARCH="${2:?Usage: fetch-busybox.sh <version> <arch> <output_path>}"
OUTPUT="${3:?Usage: fetch-busybox.sh <version> <arch> <output_path>}"
STAMP="${OUTPUT}.stamp"

# Skip if binary exists and version/arch match
if [ -f "$OUTPUT" ] && [ "$(cat "$STAMP" 2>/dev/null)" = "${VERSION}-${ARCH}" ]; then
  exit 0
fi

GCS_URL="https://storage.googleapis.com/e2b-prod-public-builds/busybox/${ARCH}/busybox"

echo "Downloading busybox v${VERSION} (${ARCH}) from GCS..."

mkdir -p "$(dirname "$OUTPUT")"
curl -sfL -o "$OUTPUT" "$GCS_URL"
chmod +x "$OUTPUT"
echo "${VERSION}-${ARCH}" > "$STAMP"
