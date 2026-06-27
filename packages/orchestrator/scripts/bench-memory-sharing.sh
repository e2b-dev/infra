#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
orchestrator_dir="$(cd "${script_dir}/.." && pwd)"
cd "${orchestrator_dir}"

: "${CONCURRENCY_LEVELS:=1,2,5,10}"
: "${BENCHTIME:=10x}"
: "${MEMORY_BENCH_SAMPLE_DELAY:=0s}"
: "${MEMORY_BENCH_CHECKPOINT:=false}"
: "${MEMORY_BENCH_PAUSE_CHECKPOINT:=false}"
: "${MEMORY_BENCH_OUTPUT_DIR:=${orchestrator_dir}/benchmarks/results/memory-sharing-$(date -u +%Y%m%dT%H%M%SZ)}"

if [[ -n "${GO_BIN:-}" ]]; then
	go_bin="${GO_BIN}"
elif ! go_bin="$(command -v go)"; then
	echo "go binary not found; set GO_BIN or install the Go version from .tool-versions" >&2
	exit 1
fi

echo "output: ${MEMORY_BENCH_OUTPUT_DIR}"
sudo --preserve-env=CONCURRENCY_LEVELS,BENCHTIME,MEMORY_BENCH_OUTPUT_DIR,MEMORY_BENCH_SAMPLE_DELAY,MEMORY_BENCH_CHECKPOINT,MEMORY_BENCH_PAUSE_CHECKPOINT,GOTOOLCHAIN,PATH \
	"${go_bin}" test ./benchmarks \
	-run='^$' \
	-bench='^BenchmarkRealFirecrackerMemorySharing$' \
	-benchtime="${BENCHTIME}" \
	-timeout=30m \
	-v
