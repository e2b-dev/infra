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
SHA512=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --version)
      VERSION="$2"
      shift 2
      ;;
    --sha512)
      SHA512="$2"
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
  amd64)
    CH_ARCH="amd64"
    [[ -n "$SHA512" ]] ||
      SHA512="32071a8d2a1c1b0071b8ae53c52782d822059c852592dbcc3f03cb14ea25afbf1a25ec12c4a7694d9edc4f2754831929251909f688146565de6e12280dd34bac"
    ;;
  arm64)
    CH_ARCH="arm64"
    [[ -n "$SHA512" ]] ||
      SHA512="d80ffde9dac48c8c812c3f88ad873ddd7625b0db6d4e34069b55bc7e8a42d9ea495422741f0ebfc10e5ffc0714059cb96834f19042922bc89f3ec9b0a8ae0ebf"
    ;;
  *)
    echo "ERROR: unsupported architecture: $DPKG_ARCH" >&2
    exit 1
    ;;
esac

BASE_URL="https://packages.clickhouse.com/tgz/stable"
TARBALL="clickhouse-common-static-${VERSION}-${CH_ARCH}.tgz"
URL="${BASE_URL}/${TARBALL}"

if [[ "$VERSION" != "25.4.5.24" || ! "$SHA512" =~ ^[0-9a-f]{128}$ ]]; then
  echo "ERROR: a reviewed --sha512 is required for this ClickHouse artifact" >&2
  exit 1
fi

echo "Installing clickhouse-client ${VERSION} (${CH_ARCH}) from ${URL}"

# Use a named work dir; DO NOT clobber $TMPDIR (POSIX env var used by curl/tar).
WORK_DIR=$(mktemp -d)
trap 'rm -rf "$WORK_DIR"' EXIT

curl -fsSL --retry 5 --retry-delay 5 -o "${WORK_DIR}/${TARBALL}" "${URL}"

# Verify against the reviewed checksum committed with the image definition.
echo "Verifying SHA-512 checksum"
echo "${SHA512}  ${WORK_DIR}/${TARBALL}" | sha512sum --check --strict

tar -xzf "${WORK_DIR}/${TARBALL}" -C "${WORK_DIR}"

# The tarball extracts to clickhouse-common-static-<VERSION>/usr/bin/clickhouse
# (a single multi-call binary; `clickhouse client`, `clickhouse local`, etc.).
EXTRACTED_BIN=$(find "${WORK_DIR}" -type f -name clickhouse -path '*/usr/bin/*' | head -n1)
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
