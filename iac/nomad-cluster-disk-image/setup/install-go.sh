#!/bin/bash
# Installs the Go toolchain from the official go.dev tarball into /usr/local/go
# and symlinks `go` and `gofmt` into /usr/local/bin.
#
# Intended to be run by Packer when building the shared Nomad cluster disk
# image so the Go toolchain is available without being downloaded at boot time.

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

# Map uname -m to the arch suffix used by the Go release artifacts.
case "$(uname -m)" in
  x86_64)  ARCH="amd64" ;;
  aarch64) ARCH="arm64" ;;
  *)
    echo "ERROR: unsupported architecture: $(uname -m)" >&2
    exit 1
    ;;
esac

TARBALL="go${VERSION}.linux-${ARCH}.tar.gz"
URL="https://go.dev/dl/${TARBALL}"

echo "Installing Go ${VERSION} (${ARCH}) from ${URL}"

WORK_DIR=$(mktemp -d)
trap 'rm -rf "$WORK_DIR"' EXIT

curl -fsSL --retry 5 --retry-delay 5 -o "${WORK_DIR}/${TARBALL}" "${URL}"

sudo rm -rf /usr/local/go
sudo tar -C /usr/local -xzf "${WORK_DIR}/${TARBALL}"
sudo ln -sf /usr/local/go/bin/go /usr/local/bin/go
sudo ln -sf /usr/local/go/bin/gofmt /usr/local/bin/gofmt

go version
