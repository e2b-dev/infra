#!/bin/bash
# Installs the ClickHouse client at a pinned version by downloading the official
# static binary tarball from https://packages.clickhouse.com/tgz/stable/.
# Intended to be run by Packer when building the shared Nomad cluster disk image
# so the client is available on every node without being downloaded at boot time.
#
# The version should be kept in sync with the ClickHouse server version (see
# `clickhouse_version` in iac/modules/job-clickhouse/variables.tf).

set -euo pipefail

VERSION=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --version)
      VERSION="$2"
      shift 2
      ;;
    *)
      echo "Unrecognized argument: $1" >&2
      exit 1
      ;;
  esac
done

if [[ -z "$VERSION" ]]; then
  echo "ERROR: --version is required" >&2
  exit 1
fi

# Map dpkg arch -> ClickHouse tarball arch suffix.
DPKG_ARCH=$(dpkg --print-architecture)
case "$DPKG_ARCH" in
  amd64) CH_ARCH="amd64" ;;
  arm64) CH_ARCH="arm64" ;;
  *)
    echo "ERROR: unsupported architecture: $DPKG_ARCH" >&2
    exit 1
    ;;
esac

BASE_URL="https://packages.clickhouse.com/tgz/stable"
TARBALL="clickhouse-common-static-${VERSION}-${CH_ARCH}.tgz"
URL="${BASE_URL}/${TARBALL}"

echo "Installing clickhouse-client ${VERSION} (${CH_ARCH}) from ${URL}"

TMPDIR=$(mktemp -d)
trap 'rm -rf "$TMPDIR"' EXIT

curl -fsSL --retry 5 --retry-delay 5 -o "${TMPDIR}/${TARBALL}" "${URL}"
tar -xzf "${TMPDIR}/${TARBALL}" -C "${TMPDIR}"

# The tarball extracts to clickhouse-common-static-<VERSION>/usr/bin/clickhouse
# (a single multi-call binary; `clickhouse client`, `clickhouse local`, etc.).
EXTRACTED_BIN=$(find "${TMPDIR}" -type f -name clickhouse -path '*/usr/bin/*' | head -n1)
if [[ -z "$EXTRACTED_BIN" ]]; then
  echo "ERROR: could not find clickhouse binary in ${TARBALL}" >&2
  exit 1
fi

INSTALL_DIR="/usr/local/bin"
sudo install -m 0755 "$EXTRACTED_BIN" "${INSTALL_DIR}/clickhouse"

# Convenience symlink so `clickhouse-client` also works (matches the apt package layout).
sudo ln -sf "${INSTALL_DIR}/clickhouse" "${INSTALL_DIR}/clickhouse-client"

# Ensure the install dir is on PATH for all shells. /usr/local/bin is on the
# default Ubuntu PATH (via /etc/environment), but drop a profile.d snippet to
# make this explicit and resilient to any image-level PATH changes.
sudo tee /etc/profile.d/clickhouse.sh > /dev/null <<EOF
# Added by install-clickhouse-client.sh
case ":\$PATH:" in
  *":${INSTALL_DIR}:"*) ;;
  *) export PATH="${INSTALL_DIR}:\$PATH" ;;
esac
EOF
sudo chmod 0644 /etc/profile.d/clickhouse.sh

# Verify the binary is reachable via PATH in a fresh login shell.
if ! command -v clickhouse >/dev/null; then
  echo "ERROR: clickhouse was not installed successfully" >&2
  exit 1
fi
if ! bash -lc 'command -v clickhouse' >/dev/null; then
  echo "ERROR: clickhouse is not on PATH for login shells" >&2
  exit 1
fi

echo "clickhouse installed at $(command -v clickhouse):"
clickhouse client --version
