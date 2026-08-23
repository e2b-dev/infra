#!/bin/bash
#
# Build minimal BusyBox static binary for E2B sandbox systeminit.
# Produces a stripped, musl-linked binary with only the applets needed
# for VM initialization (mount, init, switch_root, etc.)
#
# Usage:
#   sudo ./build.sh                          # build for host arch
#   sudo TARGET_ARCH=arm64 ./build.sh        # build for arm64
#   sudo BUSYBOX_VERSION=1.37.0 ./build.sh   # custom version
#
# Output: builds/{version}/{arch}/busybox

set -euo pipefail

BUSYBOX_VERSION="${BUSYBOX_VERSION:-1.36.1}"
TARGET_ARCH="${TARGET_ARCH:-$(uname -m)}"
HOST_ARCH="$(uname -m)"

# Normalize to Go arch convention for output dir
case "$TARGET_ARCH" in
  x86_64|amd64)   NATIVE_ARCH="x86_64"; GO_ARCH="amd64" ;;
  aarch64|arm64)   NATIVE_ARCH="aarch64"; GO_ARCH="arm64" ;;
  *) echo "ERROR: Unsupported arch: $TARGET_ARCH"; exit 1 ;;
esac

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
BUILD_DIR="${SCRIPT_DIR}/builds/${BUSYBOX_VERSION}/${GO_ARCH}"

echo "=== Building BusyBox ${BUSYBOX_VERSION} for ${GO_ARCH} (${NATIVE_ARCH}) ==="

# ── Dependencies ──────────────────────────────────────────────────────────────
apt-get update -qq
apt-get install -y -qq build-essential musl-tools linux-headers-generic curl bzip2 >/dev/null

# ── Download & verify source ──────────────────────────────────────────────────
# SHA256 checksums from https://busybox.net/downloads/SHA256SUM
declare -A BUSYBOX_SHA256=(
  ["1.36.1"]="b8cc24c9574d809e7279c3be349795c5d5ceb6fdf19ca709f80cde50e47de314"
)

SRCDIR="/tmp/busybox-src-${BUSYBOX_VERSION}-${GO_ARCH}"
if [ ! -d "$SRCDIR" ]; then
  TARBALL="/tmp/busybox-${BUSYBOX_VERSION}.tar.bz2"
  echo "Downloading BusyBox ${BUSYBOX_VERSION}..."
  curl -sL "https://busybox.net/downloads/busybox-${BUSYBOX_VERSION}.tar.bz2" -o "$TARBALL"

  EXPECTED="${BUSYBOX_SHA256[$BUSYBOX_VERSION]:-}"
  if [ -n "$EXPECTED" ]; then
    ACTUAL=$(sha256sum "$TARBALL" | cut -d' ' -f1)
    if [ "$ACTUAL" != "$EXPECTED" ]; then
      echo "ERROR: SHA256 mismatch for busybox-${BUSYBOX_VERSION}.tar.bz2"
      echo "  Expected: $EXPECTED"
      echo "  Got:      $ACTUAL"
      rm -f "$TARBALL"
      exit 1
    fi
    echo "SHA256 verified."
  else
    echo "WARNING: No SHA256 on record for version ${BUSYBOX_VERSION}, skipping verification."
  fi

  tar xjf "$TARBALL" -C /tmp
  mv "/tmp/busybox-${BUSYBOX_VERSION}" "$SRCDIR"
  rm -f "$TARBALL"
fi

cd "$SRCDIR"
make distclean 2>/dev/null || true

# ── Configure ─────────────────────────────────────────────────────────────────
# Use defconfig (full default config) to match the original embedded binary
# which had ~392 applets including ash, ip, ifconfig, cat, ln, cp, etc.
# The init script uses "#!/usr/bin/busybox ash" so ash must be present.
make defconfig

# Force static linking (defconfig defaults to dynamic)
sed -i "s/# CONFIG_STATIC is not set/CONFIG_STATIC=y/" .config

# Disable applets that don't compile with musl headers
sed -i "s/CONFIG_TC=y/# CONFIG_TC is not set/" .config

# ── Build ─────────────────────────────────────────────────────────────────────
# Native build on matching arch — use musl-gcc for static musl linking.
# musl-gcc doesn't include kernel headers (linux/vt.h etc). Copy them
# into musl's include directory from the system kernel headers.
CC="musl-gcc"
MUSL_INCLUDE=$(echo "" | musl-gcc -E -Wp,-v - 2>&1 | grep "^ /" | head -1 | tr -d " ")
if [ -z "$MUSL_INCLUDE" ] || [ ! -d "$MUSL_INCLUDE" ]; then
  echo "ERROR: cannot find musl include directory"
  exit 1
fi
echo "musl include: $MUSL_INCLUDE"
if [ ! -f "${MUSL_INCLUDE}/linux/vt.h" ]; then
  echo "Copying kernel headers to musl include path..."
  # linux/ and asm-generic/ are arch-independent
  cp -rn /usr/include/linux "${MUSL_INCLUDE}/" 2>/dev/null || true
  cp -rn /usr/include/asm-generic "${MUSL_INCLUDE}/" 2>/dev/null || true
  cp -rn /usr/include/mtd "${MUSL_INCLUDE}/" 2>/dev/null || true
  # asm/ is arch-specific: /usr/include/{triplet}/asm/ (e.g. aarch64-linux-gnu, x86_64-linux-gnu)
  if [ ! -d "${MUSL_INCLUDE}/asm" ]; then
    TRIPLET=$(gcc -dumpmachine 2>/dev/null)
    if [ -d "/usr/include/${TRIPLET}/asm" ]; then
      cp -r "/usr/include/${TRIPLET}/asm" "${MUSL_INCLUDE}/asm"
    fi
  fi
fi

# Verify critical settings survived config
grep "^CONFIG_STATIC=y" .config || { echo "ERROR: CONFIG_STATIC not set"; exit 1; }

echo "Building ($(nproc) jobs)..."
make -j"$(nproc)" CC="$CC" HOSTCC=gcc
strip busybox

# ── Verify ────────────────────────────────────────────────────────────────────
echo ""
echo "=== Result ==="
file busybox
APPLET_COUNT=$(./busybox --list | wc -l)
BINARY_SIZE=$(stat -c%s busybox)
echo "Applets: ${APPLET_COUNT}"
echo "Size: ${BINARY_SIZE} bytes ($(( BINARY_SIZE / 1024 )) KB)"
./busybox --list | sort | tr '\n' ' '
echo ""

# ── Output ────────────────────────────────────────────────────────────────────
mkdir -p "$BUILD_DIR"
cp busybox "$BUILD_DIR/busybox"
echo ""
echo "Output: ${BUILD_DIR}/busybox"
