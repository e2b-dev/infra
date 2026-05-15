#!/usr/bin/env bash
# Compare pause memfile size with vs without virtio-balloon free-page-hinting.
# Usage: bench-fph.sh <build-id> [--delay 0s] [--iterations 3] [--workload <cmd>]
# Requires root, FC v1.14+ template, and LOCAL_TEMPLATE_STORAGE_BASE_PATH.

set -euo pipefail

if [[ $EUID -ne 0 ]]; then echo "must run as root" >&2; exit 1; fi
if [[ $# -lt 1 ]]; then
  echo "usage: $0 <build-id> [--delay 0s] [--iterations 3] [--workload <cmd>]" >&2
  exit 1
fi

BUILD_ID="$1"; shift
DELAY="0s"
ITERATIONS="3"
# Allocate, touch, free ~256 MiB. Python bytearray is mmap'd by glibc so del()
# returns it to the kernel buddy allocator; the sleep lets FPR settle.
WORKLOAD='python3 -c "import time; b=bytearray(256<<20); [b.__setitem__(i,1) for i in range(0,len(b),4096)]; del b; time.sleep(1)"'

while [[ $# -gt 0 ]]; do
  case "$1" in
    --delay)      DELAY="$2"; shift 2 ;;
    --iterations) ITERATIONS="$2"; shift 2 ;;
    --workload)   WORKLOAD="$2"; shift 2 ;;
    *) echo "unknown arg: $1" >&2; exit 1 ;;
  esac
done

cd "$(dirname "$0")/.."
exec "${GO:-go}" run ./cmd/resume-build \
  -from-build "$BUILD_ID" \
  -fph-bench \
  -cmd-pause "$WORKLOAD" \
  -fph-bench-delay "$DELAY" \
  -iterations "$ITERATIONS"
