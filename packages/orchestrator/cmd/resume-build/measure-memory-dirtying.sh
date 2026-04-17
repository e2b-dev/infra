#!/bin/bash
# Measure memory/rootfs diff sizes across pause-resume cycles.
#
# This script builds a base template, then runs a series of pause-resume
# cycles to measure how many pages get dirtied during each cycle —
# both idle and under normal envd operations (file writes, process starts).
#
# Usage:
#   sudo ./measure-memory-dirtying.sh [storage-path]
#
# Requires: root, KVM, Docker, NBD, hugepages
set -euo pipefail

STORAGE="${1:-.local-build}"
CREATE_BUILD="go run ./packages/orchestrator/cmd/create-build"
RESUME_BUILD="go run ./packages/orchestrator/cmd/resume-build"

BASE_ID="measure-base-$(date +%s)"
echo "=== Step 1: Build base template ==="
$CREATE_BUILD \
  -to-build "$BASE_ID" \
  -storage "$STORAGE" \
  -hugepages \
  -v

echo ""
echo "=== Step 2: Immediate pause (baseline — no activity) ==="
LAYER_IDLE="$BASE_ID-idle"
$RESUME_BUILD \
  -from-build "$BASE_ID" \
  -to-build "$LAYER_IDLE" \
  -storage "$STORAGE" \
  -pause

echo ""
echo "=== Step 3: Resume + sleep 2s + pause (idle drift) ==="
LAYER_SLEEP="$LAYER_IDLE-sleep2"
$RESUME_BUILD \
  -from-build "$LAYER_IDLE" \
  -to-build "$LAYER_SLEEP" \
  -storage "$STORAGE" \
  -cmd-pause "sleep 2"

echo ""
echo "=== Step 4: Resume + sleep 5s + pause (longer idle drift) ==="
LAYER_SLEEP5="$LAYER_SLEEP-sleep5"
$RESUME_BUILD \
  -from-build "$LAYER_SLEEP" \
  -to-build "$LAYER_SLEEP5" \
  -storage "$STORAGE" \
  -cmd-pause "sleep 5"

echo ""
echo "=== Step 5: Resume + write files via envd + pause ==="
LAYER_WRITE="$LAYER_SLEEP5-write"
$RESUME_BUILD \
  -from-build "$LAYER_SLEEP5" \
  -to-build "$LAYER_WRITE" \
  -storage "$STORAGE" \
  -cmd-pause "dd if=/dev/urandom of=/tmp/testfile bs=1K count=64 2>/dev/null && echo written"

echo ""
echo "=== Step 6: Resume + start process via envd + pause ==="
LAYER_PROC="$LAYER_WRITE-proc"
$RESUME_BUILD \
  -from-build "$LAYER_WRITE" \
  -to-build "$LAYER_PROC" \
  -storage "$STORAGE" \
  -cmd-pause "python3 -c 'print(sum(range(10000)))' || echo 'python not available, using echo'; echo done"

echo ""
echo "=== Step 7: Multi-iteration pause benchmark (10x immediate pause) ==="
$RESUME_BUILD \
  -from-build "$LAYER_IDLE" \
  -storage "$STORAGE" \
  -pause \
  -iterations 10

echo ""
echo "=== Done ==="
echo "Compare the '📦 Artifacts' memfile/rootfs diff sizes above."
echo "Smaller diffs = fewer dirty pages = faster snapshot restore."
