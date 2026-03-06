#!/usr/bin/env bash
set -euo pipefail

# Run BenchmarkBaseImage for one or more compression modes, each in its own process.
#
# Usage:
#   sudo ./packages/orchestrator/bench.sh [modes] [extra go test flags...]
#
# Examples:
#   sudo ./packages/orchestrator/bench.sh                          # uncompressed only
#   sudo ./packages/orchestrator/bench.sh "uncompressed,zstd:2"   # two modes
#   sudo ./packages/orchestrator/bench.sh "*"                      # all modes
#   sudo ./packages/orchestrator/bench.sh "zstd:2" -benchtime=5x -count=3

ALL_MODES="uncompressed,lz4:0,zstd:1,zstd:2,zstd:3"

MODES="${1:-*}"
shift || true
EXTRA_FLAGS=("$@")

if [ "$MODES" = "*" ]; then
	MODES="$ALL_MODES"
fi

CACHE_DIR="${HOME}/.cache/e2b-orchestrator-benchmark/templates"

for mode in ${MODES//,/ }; do
	echo "=== Running mode: $mode ==="
	rm -rf "$CACHE_DIR"
	BENCH_COMPRESS="$mode" go test ./packages/orchestrator/ \
		-bench=BenchmarkBaseImage -benchtime=50x -run='^$' -timeout=60m \
		"${EXTRA_FLAGS[@]}" 2>&1 | tee "/tmp/bench-${mode//:/-}.log"
	echo ""
done
