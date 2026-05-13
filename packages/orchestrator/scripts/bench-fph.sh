#!/usr/bin/env bash
# Compare pause memfile size with vs without virtio-balloon free-page-hinting.
#
# Resumes the given build, runs a workload that dirties then frees ~256 MiB in
# the guest, pauses, and reports the resulting memfile data-layer size for
# both arms (FPR-only and FPR + FPH). FPH wins when it converts more freed
# pages into Empty mappings, shrinking the data layer.
#
# Usage: bench-fph.sh <build-id> [--delay 0s] [--iterations 3] [--workload "<cmd>"]
#
# Requires: root, FC v1.14+ template, .local-build/ checkout (or set
# LOCAL_TEMPLATE_STORAGE_BASE_PATH).

set -euo pipefail

if [[ $EUID -ne 0 ]]; then
  echo "must run as root" >&2
  exit 1
fi

if [[ $# -lt 1 ]]; then
  echo "usage: $0 <build-id> [--delay 0s] [--iterations 3] [--workload <cmd>]" >&2
  exit 1
fi

BUILD_ID="$1"
shift

DELAY="0s"
ITERATIONS="3"
# 256 MiB allocate + touch + free. Python bytearray of this size is mmap'd by
# glibc, so del() returns it directly to the kernel buddy allocator. The sleep
# gives FPR + buddy coalescing a moment to settle.
WORKLOAD='python3 - <<PY
import time
N = 256 * 1024 * 1024
b = bytearray(N)
for i in range(0, N, 4096):
    b[i] = 1
del b
time.sleep(1)
PY'

while [[ $# -gt 0 ]]; do
  case "$1" in
    --delay)      DELAY="$2"; shift 2 ;;
    --iterations) ITERATIONS="$2"; shift 2 ;;
    --workload)   WORKLOAD="$2"; shift 2 ;;
    *) echo "unknown arg: $1" >&2; exit 1 ;;
  esac
done

cd "$(dirname "$0")/.."

# Honour an explicit $GO so users on Mise/asdf can pass the right go binary
# through sudo (snap's /snap/bin/go is often the wrong version).
GO_BIN="${GO:-go}"

exec "$GO_BIN" run ./cmd/resume-build \
  -from-build "$BUILD_ID" \
  -fph-bench \
  -cmd-pause "$WORKLOAD" \
  -fph-bench-delay "$DELAY" \
  -iterations "$ITERATIONS"
