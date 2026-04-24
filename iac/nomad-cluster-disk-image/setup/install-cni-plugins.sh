#!/bin/bash
# Installs the containernetworking/plugins (CNI) binaries into /opt/cni/bin.
# Required by Nomad when running tasks in `bridge` network mode.
# Safe to install on every node even if unused, the binaries just sit in /opt/cni/bin.
#
# Intended to be run by Packer when building the shared Nomad cluster disk
# image so the plugins are available without being downloaded at boot time.

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

# Map uname -m to the arch suffix used by the CNI release artifacts.
case "$(uname -m)" in
  x86_64)  ARCH="amd64" ;;
  aarch64) ARCH="arm64" ;;
  *)
    echo "ERROR: unsupported architecture: $(uname -m)" >&2
    exit 1
    ;;
esac

URL="https://github.com/containernetworking/plugins/releases/download/${VERSION}/cni-plugins-linux-${ARCH}-${VERSION}.tgz"

echo "Installing CNI plugins ${VERSION} (${ARCH}) from ${URL}"

TMPDIR=$(mktemp -d)
trap 'rm -rf "$TMPDIR"' EXIT

curl -fsSL --retry 5 --retry-delay 5 -o "${TMPDIR}/cni-plugins.tgz" "${URL}"

sudo mkdir -p /opt/cni/bin
sudo tar -C /opt/cni/bin -xzf "${TMPDIR}/cni-plugins.tgz"

# Sanity check: bridge is the plugin Nomad needs for bridge-mode networking.
if [[ ! -x /opt/cni/bin/bridge ]]; then
  echo "ERROR: /opt/cni/bin/bridge is missing or not executable" >&2
  exit 1
fi

echo "CNI plugins installed to /opt/cni/bin:"
ls /opt/cni/bin
