# 三层快照机制与跨 microVM 内存共享实验总结

日期: 2026-06-26

## 1. 摘要

本阶段围绕 E2B Firecracker sandbox 的冷启动和内存复用做了两类工作:

- 工程实现: 引入三层快照元数据、layered template、共享 memfile 管理、CoW 私有层、layered snapshot resume 路径和后台 prefetch/reclaim 钩子。
- 实验验证: 用真实 Firecracker microVM 路径重新测量 snapshot resume 创建延迟，避免此前 dummy orchestrator 结果把 API/gRPC 开销误当成 VM 创建成本。

当前结论是: snapshot-based resume 已能把真实 Firecracker sandbox 创建保持在 300ms 级，并且在 10 并发下平均延迟仍保持在 369ms，成功率 100%。三层快照和共享 memfile 机制已经接入恢复路径，具备进一步验证跨 microVM 共享只读页、减少 per-sandbox private RSS 的基础。下一阶段重点应从 "sandbox/envd ready" 推进到 "Agent first tool ready"，并补齐 scrub/taint 校验、first-tool benchmark、intent-driven prefetch 和 launch capsule。

## 2. 背景与目标

Agent sandbox 的冷启动体验不只取决于 Firecracker 是否成功 load snapshot。用户实际感知的是创建 sandbox 后第一个工具动作何时可用，例如首次 shell stdout、Python kernel ready、文件操作返回、浏览器 ready 或 MCP tool call 完成。

本阶段先聚焦更底层的基础能力: 在不牺牲 microVM 隔离的前提下，让多个 sandbox 共享公共 runtime 状态，并把每个实例的写入限制在私有增量层中。目标包括:

- 降低真实 Firecracker snapshot resume 的创建延迟。
- 让 L0/L1 公共内存页跨 microVM 共享，避免每个 sandbox 重复占用相同只读页。
- 保留 per-sandbox CoW 隔离，确保 workspace、进程写入和实例态数据不会污染共享层。
- 为后续 Agent profile seed、first-tool prefetch 和 launch capsule 做架构铺垫。

## 3. 设计实现

### 3.1 三层快照模型

快照被拆成三类状态:

| 层级 | 名称 | 内容 | 共享范围 |
| --- | --- | --- | --- |
| L0 | Infrastructure | kernel、init、envd、base system | 跨 sandbox 共享 |
| L1 | Runtime | Python、Node、tool bridge、warmed runtime | 跨同 profile/template 共享 |
| L2 | Instance | workspace、进程 delta、dirty pages | 单实例私有 |

对应实现:

- `metadata.LayeredSnapshot` 描述 L0/L1/L2 的 layer ref、memfile path、snapfile path 和大小。
- `template.LayeredTemplate` 将 L0、L1、L2 组合成单个 `Template`，非 layered template 继续走原有路径。
- `LayeredTemplate.SharedMemfilePath()` 选择最高可共享层作为共享 memfile 入口。

### 3.2 共享 memfile

`SharedMemfileManager` 负责按 memfile path 建立共享映射。多个 VM 对同一个 memfile 使用 `mmap(MAP_PRIVATE)`:

- 只读页由 Linux host page cache 自然去重。
- 写入触发 CoW，不会修改共享文件。
- manager 使用 ref count 管理映射生命周期。
- `MAP_POPULATE` 和 `MADV_SEQUENTIAL` 用于减少首次访问 minor fault 和并发 page cache 竞争。

这个设计的关键点是把共享交给内核页缓存和 CoW 语义，而不是在用户态做页级复制或手动 dedupe。

### 3.3 CoW 私有层

`CoWOverlay` 提供 L2 私有增量层:

- overlay 文件按 base memfile 大小创建为 sparse file。
- `block.Tracker` 记录 dirty page bitmap。
- 读取时先查 overlay dirty page，未修改页回落到 shared base。
- 退出或 pause 时可通过 Firecracker dirty bitmap 找到被修改页面，再用 `process_vm_readv` 导入 overlay。
- checkpoint 保存为 `{path}.data` 和 `{path}.bitmap`，后续可用于 session resume。

这保证了 L0/L1 可以共享，同时 L2 只为实际写过的页面付费。

### 3.4 Layered resume 路径

恢复流程已经接入 `ResumeSandbox`:

1. API/orchestrator 创建 sandbox 并解析 template。
2. 如果 template 是 `LayeredTemplate`，优先调用 `createWithLayeredSnapshot`。
3. `buildMemoryLayers` 映射 L0/L1 shared memfile。
4. `ensureMergedMemfile` 将共享层预合并为一个 memfile，避免每个 VM 都生成私有 merged copy。
5. `ResumeFromLayeredSnapshot` 配置 Firecracker、rootfs、MMDS metadata 和 rate limit。
6. `loadLayeredSnapshot` 将 layered memory backend 交给 Firecracker。
7. 如果 layered resume 失败，自动 fallback 到普通 snapshot resume；普通 snapshot 失败再 fallback cold boot。

当前实现还包含后台 prefetch 和 exit reclaim 钩子:

- `startBackgroundPrefetch` 在 layered template 上预取 L0/L1 热区。
- `setupExitReclaim` 在 feature flag 开启时导入 dirty pages 并保存 L2 checkpoint。
- `setupLazyNetwork` 预留了从 L1 读取 DNS cache 的扩展点，但完整实现仍未迁移。

## 4. 实验设计

### 4.1 测试环境

| 项目 | 配置 |
| --- | --- |
| CPU | Intel Xeon E5-2660 v3 @ 2.60GHz, 40 cores |
| RAM | 157 GiB |
| OS | Linux 6.8.0-124-generic |
| Firecracker | v1.12.1 |
| VM | 2 vCPU, 512 MB RAM, 2 GB disk |
| Template | `e2bdev/base`, snapshot-based resume |
| NBD | kernel module, 64 device pool |
| Network | veth pair + tap, 10.12.0.0/16 |

### 4.2 测量口径

本次 benchmark 测量的是 `ResumeSandbox()` 到 sandbox ready 的真实 Firecracker 创建路径，覆盖:

- NBD device allocation 和 rootfs overlay。
- network slot allocation，包括 veth/tap。
- Firecracker process creation。
- snapshot loading。
- cgroup configuration。
- MMDS metadata setup。
- envd ready。

每个并发档执行 10 次迭代，每轮并发创建 N 个 sandbox，结束后清理 sandbox，warm-up 不计入结果。

需要注意: 本阶段 benchmark 仍是 sandbox ready 口径，不是 first tool ready 口径。first-tool benchmark 是下一阶段工作。

## 5. 实验结果

### 5.1 E2B 真实 Firecracker 创建结果

| 并发数 | avg | p50 | p95 | p99 | min | max | wall-clock | 成功率 |
| ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | 315ms | 322ms | 336ms | 336ms | 269ms | 346ms | 315ms | 100% |
| 2 | 319ms | 322ms | 351ms | 351ms | 271ms | 397ms | 332ms | 100% |
| 5 | 289ms | 286ms | 331ms | 337ms | 219ms | 356ms | 316ms | 100% |
| 10 | 369ms | 375ms | 436ms | 466ms | 224ms | 466ms | 402ms | 100% |

### 5.2 与 CubeSandbox 公开结果对比

| 并发数 | E2B avg | CubeSandbox avg | E2B/Cube |
| ---: | ---: | ---: | ---: |
| 1 | 315ms | 258ms | 1.22x |
| 5 | 289ms | 459ms | 0.63x |
| 10 | 369ms | 864ms | 0.43x |

观察:

- 单并发下 E2B 真实 Firecracker 路径平均 315ms，比 CubeSandbox 公开结果慢约 18%。
- 并发升高后 E2B 扩展更稳定，10 并发平均延迟只比 1 并发高约 17%。
- 10 并发下 E2B 平均延迟为 369ms，CubeSandbox 为 864ms，E2B/Cube 约 0.43x。
- 10 并发 p99 为 466ms，尾部仍在 500ms 内。

### 5.3 dummy orchestrator 结果的修正

此前 dummy orchestrator 的 28ms 级结果不能代表真实 VM 创建，因为它只覆盖:

- HTTP request/response。
- API validation。
- gRPC 调用 dummy orchestrator。
- 内存 map 写入和返回。

它没有执行 Firecracker、rootfs、network、NBD、cgroup 和 envd ready 等真实路径。因此后续冷启动讨论应以真实 Firecracker benchmark 为基线。

## 6. 结论

1. 真实 Firecracker snapshot resume 已经稳定进入 300ms 级。

   在 `e2bdev/base` snapshot-based resume 场景下，1 并发 avg 315ms，10 并发 avg 369ms，所有并发档成功率 100%。这说明当前路径具备可用的生产级基线。

2. E2B 当前瓶颈更偏单次创建延迟，而不是并发崩塌。

   单并发下仍有继续优化空间，但 5/10 并发下 tail latency 没有线性恶化。后续优化可以重点拆分 resource acquire、FC resume、envd init 和 first tool 阶段，而不是只做整体热池。

3. 三层快照机制已经打通关键链路。

   元数据、template 聚合、shared memfile 映射、Firecracker layered resume、CoW overlay 和 exit reclaim 都已有代码落点。当前机制已经可以作为 Agent profile seed 和 memory sharing 的基础。

4. 内存共享的收益还需要专门实验验证。

   本次 benchmark 证明了真实创建延迟和并发稳定性，但没有直接量化 shared pages、private RSS、dirty page ratio 和 checkpoint 大小。下一轮实验需要补充内存指标。

5. 下一阶段优化目标应升级到 first tool ready。

   Agent 场景下，用户不直接感知 "VM ready"。后续需要用 create+first command、create+Python kernel ready、create+browser ready 等口径重新建 benchmark。

## 7. 风险与边界

共享 seed 的最大风险是状态泄漏。L0/L1 只能包含公共 runtime/tool 状态，不能包含 prompt、workspace、secrets、SSH key、browser profile、命令历史、token 或实例 ID。

CoW overlay 需要严格区分 shared base 和 dirty page。任何 dirty page 导入失败都不能污染共享 memfile；checkpoint 也只能作为 session/private 状态恢复，不能跨租户复用。

Layered resume 必须保留 fallback。当前实现中 layered 失败会退回普通 snapshot resume，这个边界需要继续保留，避免实验性路径影响线上可用性。

Prefetch 不能无预算扩大。背景预取应该用 coverage/waste 指标约束，避免把内存带宽和 page cache 竞争转移到启动路径外部。

## 8. 后续计划

### Phase 0: 指标补齐

- 增加 first-tool E2E trace: API received、node selected、orchestrator create、template cache ready、resource ready、FC resume、envd init、first action first byte/complete。
- 拆分 `orchestrator.sandbox.create.duration`，明确 template/cache、network、NBD/rootfs、cgroup、FC resume、envd init 各阶段耗时。
- 新增内存指标: shared memfile resident bytes、private RSS、dirty page count、checkpoint data size。

### Phase 1: First-tool benchmark

- 增加 `create+command_first_byte`。
- 增加 `create+python_kernel_ready`。
- 增加 `create+browser_ready` 或轻量 browser dependency ready。
- 对比 sandbox ready 和 first tool ready 的差距，确认真实用户感知瓶颈。

### Phase 2: Profile seed 与 scrub

- 将 L1 明确为 Agent runtime/tool profile seed，例如 code-interpreter、node-gateway、browser、repo-agent。
- 增加 warmup manifest 和 scrub manifest。
- 发布 seed 前执行 taint/scrub validator，阻断 workspace、history、token、tmp、socket、machine-id、hostname、sandbox ID 等状态进入共享层。

### Phase 3: Intent-driven prefetch

- 将现有固定 `AgentPriorityBuilder` 替换为训练生成的 first-tool hotset。
- 将 prefetch 从 memory 扩展到 rootfs block、control channel 和 network policy。
- 按 intent key 维护 coverage、waste 和 deadline miss。

### Phase 4: Launch capsule

- 预组装 network slot、NBD/rootfs overlay、cgroup、sandbox files、Firecracker socket path 和 metrics FIFO。
- 让 NodeManager 上报 capsule inventory。
- 调度器优先选择有 profile seed、hotset 和 capsule 的节点，但加入 pressure penalty，避免热点节点成为新瓶颈。

## 9. 相关文件

- 设计文档: [`packages/orchestrator/design/agent-sandbox-cold-start.md`](../packages/orchestrator/design/agent-sandbox-cold-start.md)
- 论文版设计: [`packages/orchestrator/design/agent-sandbox-cold-start-v2-paper.md`](../packages/orchestrator/design/agent-sandbox-cold-start-v2-paper.md)
- Benchmark 结果: [`packages/orchestrator/benchmarks/e2b_real_firecracker_benchmark.md`](../packages/orchestrator/benchmarks/e2b_real_firecracker_benchmark.md)
- 汇报 PPT: [`reports/snapshot_memory_sharing_report.pptx`](snapshot_memory_sharing_report.pptx)
- PPT 生成脚本: [`tools/create_snapshot_report_ppt.py`](../tools/create_snapshot_report_ppt.py)
- Layered metadata: [`packages/orchestrator/pkg/template/metadata/template_metadata.go`](../packages/orchestrator/pkg/template/metadata/template_metadata.go)
- Layered template: [`packages/orchestrator/pkg/sandbox/template/layered.go`](../packages/orchestrator/pkg/sandbox/template/layered.go)
- Shared memfile: [`packages/orchestrator/pkg/sandbox/memory/shared_memfile.go`](../packages/orchestrator/pkg/sandbox/memory/shared_memfile.go)
- CoW overlay: [`packages/orchestrator/pkg/sandbox/memory/cow_overlay.go`](../packages/orchestrator/pkg/sandbox/memory/cow_overlay.go)
- Sandbox resume path: [`packages/orchestrator/pkg/sandbox/sandbox.go`](../packages/orchestrator/pkg/sandbox/sandbox.go)
- Firecracker layered load: [`packages/orchestrator/pkg/sandbox/fc/client.go`](../packages/orchestrator/pkg/sandbox/fc/client.go)
