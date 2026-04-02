#!/usr/bin/env bash
#
# Download busybox from e2b-dev/fc-busybox GitHub release and verify SHA256.
# Skips download if binary exists and version/arch match the stamp file.
#
# Usage:
#   ./scripts/fetch-busybox.sh <version> <arch> <output_path>
#
# Example:
#   ./scripts/fetch-busybox.sh 1.36.1 amd64 pkg/template/build/core/systeminit/busybox

set -euo pipefail

VERSION="${1:?Usage: fetch-busybox.sh <version> <arch> <output_path>}"
ARCH="${2:?Usage: fetch-busybox.sh <version> <arch> <output_path>}"
OUTPUT="${3:?Usage: fetch-busybox.sh <version> <arch> <output_path>}"
STAMP="${OUTPUT}.stamp"

# Skip if binary exists and version/arch match
if [ -f "$OUTPUT" ] && [ "$(cat "$STAMP" 2>/dev/null)" = "${VERSION}-${ARCH}" ]; then
  exit 0
fi

RELEASE_URL="https://github.com/e2b-dev/fc-busybox/releases/download/v${VERSION}"
BINARY="busybox_v${VERSION}_${ARCH}"

echo "Downloading busybox v${VERSION} (${ARCH})..."

curl -sfL -o "/tmp/${BINARY}" "${RELEASE_URL}/${BINARY}"
curl -sfL -o "/tmp/SHA256SUMS" "${RELEASE_URL}/SHA256SUMS"

(cd /tmp && grep -wF "${BINARY}" SHA256SUMS | sha256sum -c -)

mkdir -p "$(dirname "$OUTPUT")"
mv "/tmp/${BINARY}" "$OUTPUT"
chmod +x "$OUTPUT"
echo "${VERSION}-${ARCH}" > "$STAMP"
rm -f /tmp/SHA256SUMS
