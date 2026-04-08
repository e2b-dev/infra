#!/usr/bin/env bash
#
# Download busybox from e2b-dev/fc-busybox GitHub release, verify SHA256,
# and upload to GCS public builds bucket.
#
# Usage:
#   ./scripts/upload-busybox.sh <version> <arch>
#
# Example:
#   ./scripts/upload-busybox.sh 1.36.1 amd64
#   ./scripts/upload-busybox.sh 1.36.1 arm64

set -euo pipefail

VERSION="${1:?Usage: upload-busybox.sh <version> <arch>}"
ARCH="${2:?Usage: upload-busybox.sh <version> <arch>}"

RELEASE_URL="https://github.com/e2b-dev/fc-busybox/releases/download/v${VERSION}"
BINARY="busybox_v${VERSION}_${ARCH}"
GCS_PATH="gs://e2b-prod-public-builds/busybox/${VERSION}/${ARCH}/busybox"

echo "Downloading busybox v${VERSION} (${ARCH}) from GitHub..."

curl -sfL -o "/tmp/${BINARY}" "${RELEASE_URL}/${BINARY}"
curl -sfL -o "/tmp/SHA256SUMS" "${RELEASE_URL}/SHA256SUMS"

echo "Verifying SHA256..."
(cd /tmp && grep -wF "${BINARY}" SHA256SUMS | sha256sum -c -)

echo "Uploading to ${GCS_PATH}..."
gsutil -h "Cache-Control:no-cache, max-age=0" cp "/tmp/${BINARY}" "${GCS_PATH}"

rm -f "/tmp/${BINARY}" /tmp/SHA256SUMS
echo "Done."
