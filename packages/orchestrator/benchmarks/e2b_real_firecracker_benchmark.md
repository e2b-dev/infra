# E2B Real Firecracker VM Benchmark vs CubeSandbox

## Test Environment
- **CPU**: Intel Xeon E5-2660 v3 @ 2.60GHz, 40 cores
- **RAM**: 157 GiB
- **OS**: Linux 6.8.0-124-generic
- **Date**: 2026-06-11

## E2B Configuration (Real Firecracker)
- **VM Type**: Real Firecracker microVM (NOT dummy orchestrator)
- **vCPU**: 2
- **RAM**: 512 MB
- **Disk**: 2 GB
- **Kernel**: vmlinux-6.1.102
- **Firecracker**: v1.12.1_210cbac
- **Template**: e2bdev/base (snapshot-based resume)
- **NBD**: Kernel module, 64 device pool
- **Network**: veth pair + tap, 10.12.0.0/16

## Results: E2B Real Firecracker VM Creation (from snapshot)

| Concurrency | avg (ms) | p50 (ms) | p95 (ms) | p99 (ms) | min (ms) | max (ms) | wall-clock (ms) | Success Rate |
|:-----------:|:--------:|:--------:|:--------:|:--------:|:--------:|:--------:|:---------------:|:------------:|
| 1           | 315      | 322      | 336      | 336      | 269      | 346      | 315             | 100%         |
| 2           | 319      | 322      | 351      | 351      | 271      | 397      | 332             | 100%         |
| 5           | 289      | 286      | 331      | 337      | 219      | 356      | 316             | 100%         |
| 10          | 369      | 375      | 436      | 466      | 224      | 466      | 402             | 100%         |

## CubeSandbox Results (for comparison)

| Concurrency | avg (ms) | P95 (ms) | max (ms) | Success Rate |
|:-----------:|:--------:|:--------:|:--------:|:------------:|
| 1           | 258      | 307      | 325      | 100%         |
| 5           | 459      | 753      | 793      | 100%         |
| 10          | 864      | 1414     | 1417     | 100%         |

## Key Findings

### 1. Fairness Achieved
This benchmark uses **real Firecracker VMs** with full infrastructure operations:
- NBD device allocation and rootfs overlay
- Network slot allocation (veth + tap)
- Firecracker process creation via `unshare -m`
- Snapshot loading via UFFD (userfaultfd)
- Cgroup configuration
- MMDS metadata setup

Previously, E2B was benchmarked with a **dummy orchestrator** that only wrote to an in-memory Go map (~28ms), making the comparison completely unfair.

### 2. Single Concurrency Comparison
| System | avg | p50 |
|:------:|:---:|:---:|
| **E2B (Real FC)** | 315ms | 322ms |
| **CubeSandbox** | 258ms | - |

CubeSandbox is ~18% faster at single concurrency on this hardware.

### 3. Scaling Behavior
| Concurrency | E2B avg | CubeSandbox avg | E2B/Cube |
|:-----------:|:-------:|:---------------:|:--------:|
| 1           | 315ms   | 258ms           | 1.22x    |
| 5           | 289ms   | 459ms           | 0.63x    |
| 10          | 369ms   | 864ms            | 0.43x    |

**E2B scales much better under concurrency.** At 10 concurrent sandboxes:
- E2B: 369ms avg (only 17% slower than single)
- CubeSandbox: 864ms avg (3.3x slower than single)

### 4. Previous Dummy Orchestrator Data (for reference)
The dummy orchestrator showed ~28ms creation time, which was only:
- HTTP request/response overhead
- API validation logic
- gRPC call to dummy orchestrator (write a map entry)
- gRPC return

**No actual VM creation, rootfs setup, network configuration, or cgroup management.**

## Methodology
- 10 iterations per concurrency level
- Each iteration creates N sandboxes concurrently via `ResumeSandbox()`
- Measures time from call to sandbox ready (including FC resume)
- Sandboxes cleaned up after each iteration
- Warm-up run excluded from results

## Source
- Benchmark: `/users/liufy/Experiment/My-E2B/infra/packages/orchestrator/benchmarks/real_firecracker_bench_test.go`
- Results: `/users/liufy/.claude/projects/-users-liufy-Experiment/d317e139-ee8e-4b09-a2fe-e2547bd9e4a4/tool-results/bfqahmjom.txt`
