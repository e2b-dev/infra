#!/bin/bash
# Installs the containernetworking/plugins (CNI) binaries into /opt/cni/bin.
# Required by Nomad when running tasks in `bridge` network mode.
# Safe to install on every node even if unused, the binaries just sit in /opt/cni/bin.
#
# Intended to be run by Packer when building the shared Nomad cluster disk
# image so the plugins are available without being downloaded at boot time.

set -euo pipefail

VERSION=""
SHA256=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --version)
      VERSION="$2"
      shift 2
      ;;
    --sha256)
      SHA256="$2"
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

# Map uname -m to the arch suffix used by the CNI release artifacts.
case "$(uname -m)" in
  x86_64)
    ARCH="amd64"
    [[ -n "$SHA256" ]] ||
      SHA256="b8e811578fb66023f90d2e238d80cec3bdfca4b44049af74c374d4fae0f9c090"
    ;;
  aarch64)
    ARCH="arm64"
    [[ -n "$SHA256" ]] ||
      SHA256="01e0e22acc7f7004e4588c1fe1871cc86d7ab562cd858e1761c4641d89ebfaa4"
    ;;
  *)
    echo "ERROR: unsupported architecture: $(uname -m)" >&2
    exit 1
    ;;
esac

TARBALL="cni-plugins-linux-${ARCH}-${VERSION}.tgz"
BASE_URL="https://github.com/containernetworking/plugins/releases/download/${VERSION}"
URL="${BASE_URL}/${TARBALL}"

if [[ "$VERSION" != "v1.6.2" || ! "$SHA256" =~ ^[0-9a-f]{64}$ ]]; then
  echo "ERROR: a reviewed --sha256 is required for this CNI artifact" >&2
  exit 1
fi

echo "Installing CNI plugins ${VERSION} (${ARCH}) from ${URL}"

# Use a named work dir; DO NOT clobber $TMPDIR (POSIX env var used by curl/tar).
WORK_DIR=$(mktemp -d)
trap 'rm -rf "$WORK_DIR"' EXIT

curl -fsSL --retry 5 --retry-delay 5 -o "${WORK_DIR}/${TARBALL}" "${URL}"

# Verify against the reviewed checksum committed with the image definition.
echo "Verifying SHA-256 checksum"
echo "${SHA256}  ${WORK_DIR}/${TARBALL}" | sha256sum --check --strict

sudo mkdir -p /opt/cni/bin
sudo tar -C /opt/cni/bin -xzf "${WORK_DIR}/${TARBALL}"

# Sanity check: bridge is the plugin Nomad needs for bridge-mode networking.
if [[ ! -x /opt/cni/bin/bridge ]]; then
  echo "ERROR: /opt/cni/bin/bridge is missing or not executable" >&2
  exit 1
fi

echo "CNI plugins installed to /opt/cni/bin:"
ls /opt/cni/bin
