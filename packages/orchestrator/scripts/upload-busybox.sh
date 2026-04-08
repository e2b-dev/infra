#!/usr/bin/env bash
#
# Download busybox from e2b-dev/fc-busybox GitHub release, verify SHA256,
# and upload to GCS public builds bucket with a SHA256 sidecar.
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
GCS_PREFIX="gs://e2b-prod-public-builds/busybox/${VERSION}/${ARCH}"

echo "Downloading busybox v${VERSION} (${ARCH}) from GitHub..."

curl -sfL -o "/tmp/${BINARY}" "${RELEASE_URL}/${BINARY}"
curl -sfL -o "/tmp/SHA256SUMS" "${RELEASE_URL}/SHA256SUMS"

echo "Verifying SHA256..."
(cd /tmp && grep -wF "${BINARY}" SHA256SUMS | sha256sum -c -)

# Generate sidecar checksum file for the binary with canonical name
SHA256=$(sha256sum "/tmp/${BINARY}" | cut -d' ' -f1)
echo "${SHA256}  busybox" > "/tmp/busybox.sha256"

echo "Uploading to ${GCS_PREFIX}/..."
gsutil -h "Cache-Control:no-cache, max-age=0" cp "/tmp/${BINARY}" "${GCS_PREFIX}/busybox"
gsutil -h "Cache-Control:no-cache, max-age=0" cp "/tmp/busybox.sha256" "${GCS_PREFIX}/busybox.sha256"

rm -f "/tmp/${BINARY}" /tmp/SHA256SUMS /tmp/busybox.sha256
echo "Done."
