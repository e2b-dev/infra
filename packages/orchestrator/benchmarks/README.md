# Orchestrator Benchmarks

## Memory sharing benchmark

`BenchmarkRealFirecrackerMemorySharing` 用真实 Firecracker resume 路径创建 sandbox，并在 sandbox 关闭前采集内存共享相关指标。它补充 `BenchmarkRealFirecracker` 没覆盖的四类数据：

- shared pages: 从 Firecracker 进程的 `/proc/<pid>/smaps_rollup` 读取 `Shared_Clean + Shared_Dirty + Shared_Hugetlb`。
- private RSS: 从 `smaps_rollup` 读取 `Private_Clean + Private_Dirty + Private_Hugetlb`。
- dirty page ratio: 主要使用 Firecracker dirty bitmap 计算 `dirty_bytes / guest_memory_bytes`，同时记录 host 侧 `smaps` dirty RSS ratio。
- checkpoint size: 可选采集 layered reclaim 生成的 `{path}.data` 和 `{path}.bitmap`，并记录 logical bytes 与 sparse file allocated bytes。

### 前置条件

需要在能运行真实 microVM 的 Linux host 上执行：

```bash
sudo modprobe nbd
```

确认以下本地缓存已经存在，和现有真实 Firecracker benchmark 一致：

- Firecracker binary: `fc-versions/builds/v1.12.1_210cbac`
- kernel: benchmark 会从 `https://storage.googleapis.com/e2b-prod-public-builds/kernels/vmlinux-6.1.102/vmlinux.bin` 下载到本地 cache，已有文件会复用
- template cache: `LOCAL_TEMPLATE_STORAGE_BASE_PATH` 下存在 build `ba6aae36-74f7-487a-b6f7-74fd7c94e479`

### 运行

推荐用包装脚本：

```bash
cd packages/orchestrator
CONCURRENCY_LEVELS=1,2,5,10 \
BENCHTIME=10x \
MEMORY_BENCH_SAMPLE_DELAY=1s \
./scripts/bench-memory-sharing.sh
```

等价的原始命令：

```bash
cd packages/orchestrator
sudo --preserve-env=CONCURRENCY_LEVELS,MEMORY_BENCH_OUTPUT_DIR,MEMORY_BENCH_SAMPLE_DELAY,MEMORY_BENCH_CHECKPOINT,MEMORY_BENCH_PAUSE_CHECKPOINT,GOTOOLCHAIN,PATH \
  "$(which go)" test ./benchmarks \
  -run='^$' \
  -bench='^BenchmarkRealFirecrackerMemorySharing$' \
  -benchtime=10x \
  -timeout=30m \
  -v
```

### 常用环境变量

- `CONCURRENCY_LEVELS`: 并发档位，默认 `1,2,5,10`。
- `BENCHTIME`: 每个并发档的重复次数，包装脚本默认 `10x`。
- `MEMORY_BENCH_OUTPUT_DIR`: 输出目录；默认是 `packages/orchestrator/benchmarks/results/memory-sharing-<timestamp>`。
- `MEMORY_BENCH_SAMPLE_DELAY`: sandbox ready 后等待多久再采样，默认 `0s`。建议正式实验用 `1s` 到 `3s`，降低刚启动时瞬态 page fault 的影响。
- `MEMORY_BENCH_CHECKPOINT`: 设为 `true` 后，在关闭 sandbox 后 stat layered reclaim checkpoint 文件。只有 layered template 且 smart reclaim 路径实际生成 checkpoint 时才会有数据。
- `MEMORY_BENCH_PAUSE_CHECKPOINT`: 设为 `true` 后额外执行 `Sandbox.Pause()` 并记录 pause snapshot 产物大小。这个路径会导出内存和 rootfs diff，成本明显更高，不建议混入常规 RSS/shared page 实验。

### 输出

每次运行会生成：

- `samples.jsonl`: 每个 sandbox 一行，包含 `shared_rss_bytes`、`private_rss_bytes`、`guest_dirty_pages`、`guest_dirty_ratio`、`host_dirty_rss_ratio`、`checkpoint`、`pause_snapshot` 等字段。
- `summary.csv`: 每个并发档一行，包含平均 latency、平均 shared RSS、平均 private RSS、平均 dirty ratio 和平均 checkpoint allocated size。

解释 checkpoint 大小时优先看 allocated bytes。`.data` 是 sparse file，logical size 可能接近 guest memory size，但真实磁盘占用由 dirty pages 决定。

### 建议实验矩阵

先跑 baseline：

```bash
cd packages/orchestrator
CONCURRENCY_LEVELS=1,2,5,10 BENCHTIME=10x MEMORY_BENCH_SAMPLE_DELAY=1s ./scripts/bench-memory-sharing.sh
```

再跑 checkpoint：

```bash
cd packages/orchestrator
CONCURRENCY_LEVELS=1,5,10 BENCHTIME=5x MEMORY_BENCH_SAMPLE_DELAY=1s MEMORY_BENCH_CHECKPOINT=true ./scripts/bench-memory-sharing.sh
```

如果需要量化完整 pause snapshot 大小，单独跑低并发：

```bash
cd packages/orchestrator
CONCURRENCY_LEVELS=1 BENCHTIME=3x MEMORY_BENCH_SAMPLE_DELAY=1s MEMORY_BENCH_PAUSE_CHECKPOINT=true ./scripts/bench-memory-sharing.sh
```
