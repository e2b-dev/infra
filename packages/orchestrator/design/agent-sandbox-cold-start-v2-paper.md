# V2: 面向论文的 Agent Sandbox 冷启动优化设计

日期: 2026-06-26

暂定论文题目: **First Tool Fast: Intent-Aware Cold Starts for Agentic MicroVM Sandboxes**

## 1. 研究问题

现有 serverless cold-start 研究主要把 "函数实例启动" 作为对象。AFaaS 的重要贡献是把端到端冷启动重新定义为从节点收到扩容请求，到实例创建、代码加载、请求执行并返回终端用户响应的完整路径，并将瓶颈拆为控制路径、资源争用、用户代码初始化三类。

Agent sandbox 的冷启动问题不同。用户并不关心 microVM 是否已经恢复，也不关心 envd 是否已经可以响应 `/init`。用户感知的是 Agent 的第一个工具动作何时产生可用结果，例如:

- 第一次 shell command 返回首字节 stdout。
- Python code interpreter 的 kernel ready 并执行第一段代码。
- 文件系统写入和读取可见。
- 浏览器/GUI sandbox 到达 first interactive state。
- MCP/tool server 完成 schema load 并能处理第一个 tool call。

因此本文研究一个更窄、更适合产出论文的问题:

> 在强隔离的 Firecracker microVM 中，如何利用 Agent 工作负载的语义可预测性，把 cold start 从 "sandbox ready" 优化为 "first tool result ready"，同时保持跨租户安全、资源效率、工业可部署性，并默认不要求用户修改 Agent 代码或 sandbox 内代码？

这个问题和 AFaaS 的差异是: AFaaS 优化通用函数从 runtime request 到 handler response 的路径；本文优化 Agent control plane 已知或可预测的 first tool action，并把 first tool action 纳入 sandbox 启动协议、调度、snapshot 安全和预取训练。

## 2. 相关工作边界

AFaaS 提出了 FRI、资源池化/共享、树状 seed，以解决通用 FaaS 冷启动中的控制路径、资源争用和用户代码初始化。本文不把这些重新包装为创新，而把它们作为 baseline。

Firecracker 提供快速 microVM 和 snapshot 基础，但其论文和官方文档都明确指出，Firecracker 本身不提供 orchestration、snapshot lifecycle management 或更高层的 packaging。它支持通过 `MAP_PRIVATE` 从 memory file 按需加载 snapshot 页面，但 snapshot 文件的安全、分发和生命周期由上层系统负责。

Kubernetes SIG Apps 的 Agent Sandbox 提供 CRD、SandboxTemplate、SandboxWarmPool 等 agent sandbox 生命周期抽象。它解决的是 Kubernetes 上的声明式管理和 warm pool claim 问题，不解决 microVM snapshot 中 first-tool hot path、跨租户 seed 安全或 Agent intent 和 sandbox runtime 的协同。

近期 OS co-design 和 SnapStart 工作继续优化 snapshot restore、page fault、memory dedup 和 keep-alive 经济性，但大多仍以函数或 VM restore 为核心，不把 Agent 的第一个工具动作作为优化目标。

本文应避免声称 "第一次提出 snapshot/prefetch/warm pool"。真正的论文贡献应是 Agent-specific 的语义接口、安全 seed 切分和 first-tool 端到端优化。

## 3. 论文贡献

本文至少应有四个实质创新点，其中前三个必须作为核心机制贡献，第四个可作为系统化贡献。

可写进论文 Introduction 的贡献列表:

1. 提出 first-tool cold start 作为 Agent sandbox 的端到端冷启动口径，并给出 AgentColdBench benchmark。
2. 提出 Transparent Agent Intent Contract，把 first-tool 语义以推断、SDK 内部 hint 或可选显式 hint 的方式暴露给调度、snapshot、prefetch 和 runtime init。
3. 提出 Taint-Safe Agent Profile Seeds，在多租户 Agent 场景下安全共享 warmed tool state。
4. 提出 First-Tool Multi-Modal Hotsets，用 first-tool intent 训练 memory/rootfs/control/network-policy 热路径。
5. 提出 Deadline-Aware Launch Capsules，将多种启动资源组合为调度器可见的 deadline-aware resource escrow。

与 AFaaS 的 novelty 对照:

| 维度 | AFaaS | 本文 |
| --- | --- | --- |
| E2E 口径 | scheduler request 到函数响应 | API create 到 first tool first byte/complete |
| 控制路径 | FRI 缩短 runtime 间 shim-call | T-AIC 在不要求用户改代码的情况下将 first-tool 语义并入 create/init/route |
| seed | 函数/用户代码 tree seed | taint-safe Agent profile seed，按 public/team/session scope 共享 |
| 预取 | seed/fork 后减少初始化成本 | first-tool intent keyed memory/rootfs/control/network-policy hotset |
| 资源池 | veth/cgroup 等单资源 pool | 多资源 launch capsule，调度器可见并按 deadline 管理 |
| 安全重点 | seed 数据与 sandbox 隔离 | 防止 prompt/repo/secret/browser/session state 进入共享 seed |
| 评估对象 | serverless 函数 benchmark | shell/python/repo/browser/MCP 等 Agent first-tool workload |

### 贡献一: Transparent Agent Intent Contract

提出 **Transparent Agent Intent Contract, T-AIC**。T-AIC 是 Agent control plane 和 sandbox runtime 之间的新启动协议。它的目标不是要求用户在 create 时手写 first-tool 声明，而是让平台以透明或半透明方式获得 first-tool 语义，并把这些语义用于调度、seed 选择、hotset 选择和 runtime init。

T-AIC 有四个兼容级别:

- L0 平台透明: 用户代码、SDK 调用方式和 sandbox 内代码都不变。系统从历史 trace、template/profile 默认行为和线上 shadow collection 推断 first-tool distribution。
- L1 SDK 透明: 用户代码不变，但 SDK 在现有 API 调用中自动携带 profile/default intent。例如 code-interpreter SDK 知道下一步大概率是 Python execution，browser SDK 知道下一步大概率是 browser ready。
- L2 模板/运维 hint: 模板作者或平台为 template 标注 profile、warmup scenario、常见 first tool。使用该模板的用户代码不变。
- L3 显式 hint: 高阶用户可在 create 时传入 first tool hint 以获得更确定的低延迟，但这是优化选项，不是正确性要求。

L3 显式 hint 示例:

```json
{
  "profile": "code-interpreter/python",
  "first_tool": "python.exec",
  "first_tool_shape": {
    "imports": ["pandas", "numpy"],
    "expects_stdout": true,
    "deadline_ms": 200
  },
  "state_scope": "fresh",
  "network_shape": "default-deny-public-pypi"
}
```

T-AIC 不是简单的 API 字段扩展。它改变三个系统决策:

- 调度器选择有对应 profile seed、hotset 和 launch capsule 的节点。
- orchestrator 在 sandbox 还未对外返回前提前准备 first-tool runtime，例如 Python kernel、Node gateway、browser dependency mmap。
- envd 在 `/init` 之后直接进入 first-tool-ready 状态，或在安全条件满足时执行 first action，并把 first-byte/complete 纳入同一个 trace。

这和 AFaaS FRI 的区别:

- FRI 优化 high-level runtime 到 low-level runtime 的 intra-node control path。
- T-AIC 优化 Agent API 到 sandbox first-tool 的 semantic control path。
- FRI 不关心请求语义；T-AIC 通过推断或 hint 把 first-tool 类型、deadline、profile 和状态作用域传入启动系统。
- T-AIC 的正确性不依赖 intent 命中。缺失或错误 intent 只会降低命中率，系统必须回退到普通 create/run 路径。

可验证假设:

- H1: 对 create 后立即 command/runCode 的 Agent workload，T-AIC 能在不修改用户代码的 L0/L1 模式下降低 `envd_ready -> first_tool_first_byte` 延迟。
- H2: 显式 hint 相比推断 hint 能进一步提高调度命中率和预取覆盖率，但不是获得收益的必要条件。

工程落点:

- API: `PostSandboxes` 增加实验性 optional `agentIntent`，同时支持服务端根据 template/profile 自动填充 inferred intent。
- SDK: 高阶 SDK 可在不改变用户代码的情况下携带 profile/default intent；低阶 SDK 保持兼容。
- gRPC: `SandboxCreateRequest` 增加 optional intent message，缺失时走 inferred/default intent。
- orchestrator: `Server.Create` 将 explicit 或 inferred intent 传入 `ResumeSandbox`。
- envd: 新增 `POST /init-intent` 或 `/init` 后的 one-shot first-tool queue。

### 贡献二: Taint-Safe Agent Profile Seeds

提出 **Taint-Safe Agent Profile Seeds, TSAPS**。目标是在多租户 Agent sandbox 中共享已初始化的工具状态，同时证明不会把用户状态带入共享 seed。

AFaaS 的 tree-structured seed 可以包含用户代码，但 Agent 的风险更高: prompt、repo、credentials、browser session、MCP token、命令历史、临时文件都可能进入内存或 rootfs。TSAPS 的创新点是给 seed 引入语义状态分区和 taint gate。

状态分区:

- Public state: 公共 runtime、公共工具、公共 package metadata，可跨租户共享。
- Team state: 团队私有依赖和 package cache，只能同团队共享。
- Session state: workspace、prompt、secrets、命令历史、browser profile，只能同 sandbox/session 恢复。
- Ephemeral state: pid/socket/tmp/lock/random/hostname/sandbox ID，每次实例化必须重建。

Taint gate:

- 文件系统层: envd/template build 记录 write set。只有 allowlist 路径可以进入 Public/Team seed；其他 dirty path 自动 taint。
- 进程层: warmup 命令运行在 seed-builder cgroup/process group 中，用户命令和 secret injection 永远发生在 snapshot 之后。
- 环境层: token、env vars、MMDS metadata、CA bundle、volume mounts 都是 resume-time material，不写入 profile seed。
- 校验层: seed 发布前执行 scrub validator，恢复两次并比较 sandbox ID、hostname、history、tmp、token paths 是否实例化成功。

这和 AFaaS seed tree 的区别:

- AFaaS 关注 seed 层级和 CoW memory efficiency。
- TSAPS 关注 Agent 多租户 seed 的安全可共享性，把 "哪些状态可共享" 变成系统可检查属性。
- TSAPS 不把任意用户代码 seed 跨租户共享，而是共享工具 profile 的 public/team scoped warmed state。

可验证假设:

- H3: 对 code-interpreter/browser/repo-agent profile，TSAPS 能在不泄漏 session state 的情况下复用 runtime/tool 初始化状态。
- H4: 相比 per-template full snapshot，TSAPS 可降低增量 private RSS，并保持高 profile hit rate。

工程落点:

- metadata: 增加 `agent_profile_id`、`seed_scope`、`warmup_manifest`、`scrub_manifest`、`taint_summary`。
- build: finalize/optimize phase 增加 profile warmup 和 scrub validator。
- sandbox/template: 复用 `LayeredTemplate`、`SharedMemfileManager`、`CoWOverlay`。
- API: team-scoped seed 需要显式 opt-in，并受权限控制。

### 贡献三: First-Tool Multi-Modal Hotsets

提出 **First-Tool Multi-Modal Hotsets, FTMH**。现有 prefetch 常以 VM restore 或 function init 为目标，主要关注 memory page。Agent first-tool 路径同时跨越 memory、rootfs 和网络策略。

FTMH 为每种 first tool intent 训练一个多模态 hotset:

- Memory hotset: UFFD fault trace，记录 first-tool 前访问的 guest memory blocks。
- Rootfs hotset: NBD/rootfs read trace，记录 package metadata、binary、shared library、font/browser data、tool schema 文件块。
- Control hotset: envd/proxy/port/gateway route 的建立顺序。
- Network policy hotset: DNS allowlist、firewall rule、egress proxy route 的预编译和预安装，不共享真实连接。

Hotset 不是无预算全量预取，而是 deadline-aware:

- P0: envd init 和 first-tool control channel 必需页/块。
- P1: first byte 前必需 runtime/tool 路径。
- P2: first tool complete 前高概率路径。
- P3: 后续交互可能需要的后台路径。

FTMH 的在线策略:

- 每个 hotset 维护 `coverage`、`waste`、`deadline_miss`。
- 当 waste 过高时自动缩小 P1/P2。
- 当 first-tool demand fault 命中未预取路径时提升优先级。
- 当 intent shape 改变，例如 imports 改变或 browser profile 改变时，生成新的 hotset key。

这和 AFaaS/常见 prefetch 的区别:

- AFaaS 的 seed 解决用户代码初始化，未以 first tool action 为训练边界。
- PASS/FaaSnap 类工作关注 microVM snapshot page fault 或 workload active pages；FTMH 把 Agent tool intent 作为 hotset key，并联合 memory/rootfs/control/network policy。
- FTMH 用 coverage/waste/deadline 约束工业部署成本，避免 "prefetch 越多越好"。

可验证假设:

- H5: first-tool intent keyed hotset 相比 template-level hotset 有更高 prefetch coverage 和更低 waste。
- H6: rootfs hotset 对 Python/Node/browser first action 的收益与 memory hotset 同等重要，单独 memory prefetch 不足以优化 E2E first-tool latency。

工程落点:

- `block.PrefetchData` 扩展 rootfs read trace 或新增 rootfs hotset collector。
- `MemoryPrefetchMapping` 增加 `intent_key`、`phase`、`priority`、`confidence`。
- `OptimizeBuilder` 从 "resume 到 envd ready" 扩展到 "resume 到 first-tool first byte/complete"。
- `sandbox.Factory.ResumeSandbox` 在 resource acquire 并行阶段启动 P0/P1 hotset 预取。

### 贡献四: Deadline-Aware Launch Capsules

提出 **Deadline-Aware Launch Capsules, DALC**。AFaaS 将 veth、cgroup 等资源分别池化。E2B 已有 network pool 和 NBD device pool。DALC 的创新是把多种启动资源按 intent/profile 组合成原子胶囊，并由调度器感知其 deadline 和命中状态。

Capsule 包含:

- network slot 和可快速切换的 egress/firewall policy handle。
- rootfs overlay skeleton 和 NBD slot。
- cgroup directory/FD。
- sandbox files 目录、Firecracker socket path、metrics FIFO。
- 可选 prefetch budget 和 hotset state。

Capsule 不一定预启动 Firecracker process。Firecracker pre-spawn 可作为后期实验，因为它会提高生命周期复杂度。早期 DALC 的重点是减少多资源组合等待和内核争用。

Capsule key:

```text
{cluster, arch, kernel_version, firecracker_version, template_build_id,
 agent_profile_id, first_tool_type, network_policy_shape, memory_mb, vcpu}
```

调度器将 capsule 当成可消费资源:

- capsule hit: 该节点对特定 intent 有接近 ready 的启动资源。
- capsule warmable: 该节点有 seed 和 hotset，但没有 ready capsule，可后台准备。
- capsule miss: 普通路径。

这和 warm pool 的区别:

- warm pool 保留完整 sandbox/pod，资源占用高且容易包含状态。
- DALC 保留未实例化的启动资源，不包含用户进程和用户 workspace。
- DALC 与 T-AIC/FTMH 结合，按 first-tool deadline 和 profile 动态决定预留深度。

可验证假设:

- H7: DALC 可降低高并发 Agent burst 下的 resource acquire p95/p99。
- H8: 相同资源预算下，DALC 比 full warm sandbox pool 支持更多 profile，并有更低状态泄漏风险。

工程落点:

- 增加 `LaunchCapsule` interface。
- `ResumeSandbox` 支持 `WithLaunchCapsule`。
- cgroup 增加 pool。
- NodeManager 汇报 capsule inventory。
- placement scoring 增加 capsule locality 和 pressure penalty。

### 贡献五: AgentColdBench

提出 **AgentColdBench**，用于评估 Agent sandbox 的 first-tool cold start。该 benchmark 不是单独追求覆盖更多 workloads，而是提供新口径下的可重复评价方法。

AgentColdBench 包含:

- Workload suite: shell、Python data、repo-agent、Node tool、browser-lite、MCP bridge。
- State modes: fresh create、profile seed hit、capsule hit、session resume。
- Metrics: first byte、first complete、coverage、waste、private RSS、resource wait。
- Tracing schema: 统一 API、scheduler、orchestrator、envd、client-proxy、first-tool 的 trace event。
- Safety suite: seed scrub、token/history/workspace leakage、恢复唯一性检查。

这和传统 serverless benchmark 的区别:

- 不只测 handler 执行时间，而测 create 后的第一个工具动作。
- 不只测 language runtime import，而测 Agent 工具路径中的 filesystem、process、IPC、proxy 和 network policy。
- 不只测平均延迟，而同时测 resource budget、prefetch waste 和 seed safety。

可验证假设:

- H9: 在同样的 sandbox ready 延迟下，不同系统的 first-tool latency 可以相差显著，因此 AgentColdBench 能揭示传统 cold-start metric 看不到的问题。

工程落点:

- 在 `packages/orchestrator/benchmarks` 增加 first-tool benchmark。
- 在 tests/periodic-test 增加 SDK 级 create+first action 端到端用例。
- trace schema 进入 shared telemetry，便于线上 shadow collection。

## 4. 系统架构

整体系统命名为 **AIFastSandbox**，由五个模块组成:

1. Intent Frontend: API/SDK 解析 Agent intent，并生成 normalized intent key。
2. Profile Seed Builder: 构建 TSAPS seed，执行 warmup、scrub、taint gate 和 snapshot publish。
3. Hotset Trainer: 在 build-time 和 shadow runtime 收集 first-tool multi-modal hotset。
4. Capsule Manager: 按 intent/profile 维护 DALC 胶囊，并在 pressure 下自动降级。
5. Profile-Aware Scheduler: 在 API/node manager 层根据 seed、hotset、capsule 和资源压力选择节点。

启动路径:

1. 用户按现有方式调用 create 或 create-and-run；如果用户没有显式 hint，SDK/API 根据 profile、template 和历史 trace 生成 inferred intent。
2. API resolve template/build，并计算 explicit 或 inferred intent key。
3. scheduler 查找有 profile seed/hotset/capsule 的节点。
4. orchestrator 收到 create 请求，优先获取 matching capsule。
5. 同步加载 TSAPS seed，启动 P0/P1 FTMH 预取。
6. Firecracker resume。
7. envd init 注入实例态 material。
8. envd 执行或预热 first tool。
9. API/SDK 得到 first byte 或 first result。

Fallback:

- 无 intent: 退化为普通 E2B create。
- 无 profile seed: 退化为 template snapshot。
- 无 hotset: 只做 envd-ready prefetch。
- 无 capsule: 使用现有 network/NBD pool。
- taint validation fail: seed 不发布，线上不受影响。

## 5. 透明度与兼容性

透明度是本文能否工业部署的关键。最终系统应遵循:

> 用户代码不应为了获得正确性而修改；显式 intent 只用于提高性能确定性。

这里的 "用户代码" 分三层:

- Agent application code: 用户写的 `Sandbox.create()`、`sandbox.commands.run()`、`sandbox.runCode()` 等调用。
- Sandbox workload code: 在 microVM 内运行的 Python/Node/shell/repo/browser 代码。
- Template/operator configuration: 平台或模板作者维护的 profile、warmup、scrub 配置。

默认目标:

- Agent application code 不需要修改。L0/L1 模式下，现有 create-then-run 序列保持可用。
- Sandbox workload code 不需要修改。预热、scrub、init 和 first-tool queue 由 envd/orchestrator 处理。
- Template/operator configuration 可以修改，因为这是平台部署的一部分，不应算作用户代码侵入。

各机制透明度:

| 机制 | 用户 Agent 代码 | Sandbox 内代码 | SDK/平台改动 | 备注 |
| --- | --- | --- | --- | --- |
| First-tool measurement | 不改 | 不改 | 需要 | 纯观测，可先 shadow |
| T-AIC L0 inferred intent | 不改 | 不改 | 需要 | 根据历史 trace/profile 默认推断 |
| T-AIC L1 SDK intent | 不改 | 不改 | 需要 SDK 升级 | SDK 自动携带 profile/default hint |
| T-AIC L3 explicit hint | 可选修改 | 不改 | 需要 | 只为更高命中率 |
| TSAPS | 不改 | 不改 | 需要 build/scrub pipeline | seed warmup 是平台任务 |
| FTMH | 不改 | 不改 | 需要 trainer/prefetcher | intent 缺失时用 profile default |
| DALC | 不改 | 不改 | 需要 orchestrator/scheduler | 资源胶囊对用户不可见 |
| CreateAndRun streaming | 可选改用新 API | 不改 | 需要 SDK/API | 长期优化，不是基础收益前提 |

因此，论文里的主评估应至少报告三种模式:

- Transparent: 不修改用户 Agent 代码，只升级平台/SDK，使用 inferred/default intent。
- Annotated: 模板或 profile 标注 first-tool distribution，用户代码不变。
- Explicit: 用户传入 first-tool hint 或使用 create-and-run API。

如果只有 Explicit 模式有效，论文说服力会较弱；真正有价值的结果应证明 Transparent/Annotated 已经能获得主要收益，Explicit 只是上界。

## 6. E2B 实现计划

### Phase A: First-Tool Measurement

目标: 建立论文的核心评价口径。

实现:

- 增加 `agent.sandbox.first_tool.duration`。
- 在 SDK/API/client-proxy/envd/orchestrator 串联 trace ID。
- benchmark 增加 `create+command_first_byte`、`create+python_kernel_ready`、`create+browser_ready`。
- 拆解 API scheduling、template cache、resource acquire、FC resume、envd init、first-tool。

论文产出:

- Agent sandbox cold start characterization。
- 证明 "envd ready" 和 "first-tool ready" 差距显著。

### Phase B: T-AIC Prototype

目标: 验证语义 intent 能缩短控制路径。

实现:

- API/gRPC 增加 optional `AgentIntent`，并支持缺省时由服务端生成 inferred intent。
- SDK 在不改变用户代码的前提下携带 high-level profile/default intent。
- envd 增加 first-tool warmup queue。
- 对 `commands.run` 和 `python.exec` 先实现。
- create 响应可携带 `first_tool_ready_at`。

论文产出:

- 对比 create-then-run 两次请求、T-AIC inferred intent 和显式 hint。
- 分析 second request routing/proxy/envd startup 被消除的延迟。

### Phase C: TSAPS Builder

目标: 安全共享 profile seed。

实现:

- 先支持 global public `code-interpreter/python` seed。
- warmup 执行: start Python kernel skeleton、import stdlib/numpy/pandas optional、load envd process APIs。
- scrub validator 检查路径和实例态。
- metadata 写入 taint summary。

论文产出:

- seed safety validation。
- memory sharing 和 first-tool latency 收益。

### Phase D: FTMH Trainer

目标: first-tool hotset 训练和部署。

实现:

- build optimize phase 执行 first-tool scenarios。
- memory hotset 使用现有 UFFD prefetch data。
- rootfs hotset 从 block/rootfs provider 添加 read tracking。
- prefetch mapping 增加 intent/priority。
- resume 时按 P0/P1/P2 budget 预取。

论文产出:

- memory-only vs rootfs-only vs multi-modal hotset 消融实验。
- coverage/waste/deadline tradeoff。

### Phase E: DALC and Scheduling

目标: 并发和工业部署潜力。

实现:

- capsule 先包含 network slot、NBD/rootfs overlay、cgroup、sandbox files。
- capsule inventory 上报 NodeManager。
- placement 加 locality score。
- capsule preparation lane 限速，避免 noisy neighbor。

论文产出:

- 高并发 burst p95/p99 改善。
- 与 full warm pool 的资源效率对比。

## 7. 实验设计

### Baselines

- E2B current: 当前 snapshot resume + network/NBD pool。
- E2B + template cache hot。
- E2B + existing memory prefetch。
- Warm sandbox pool: 保留完整已启动 sandbox。
- AFaaS-style approximation: resource pooling + profile snapshot，但无 Agent intent 和 first-tool hotset。

### Workloads

最小论文 workload:

- `shell-basic`: create 后执行 `echo`、`python --version`、`ls`。
- `python-data`: create 后执行 Python，import `numpy/pandas` 并计算小任务。
- `repo-agent`: create 后 clone 或 mount repo，执行 `pytest -q` 或 `ripgrep`。
- `node-tool`: create 后启动 Node gateway 并执行 JSON RPC。
- `browser-lite`: create 后启动 browser dependency path，到 first page ready。

这些 workload 覆盖 shell、Python、filesystem metadata、Node runtime、browser data 五类 first-tool pattern。

### Metrics

Latency:

- API received 到 sandbox ID returned。
- API received 到 envd ready。
- API received 到 first tool first byte。
- API received 到 first tool complete。
- p50/p95/p99 under concurrency。

Resource:

- private RSS per sandbox。
- shared page cache bytes。
- seed/capsule memory budget。
- NBD/network/cgroup wait time。
- prefetch bytes and waste。

Safety:

- scrub validator false positive/false negative。
- seed taint rejection rate。
- repeated restore uniqueness checks。
- token/history/workspace leakage tests。

### Ablation

- No T-AIC。
- T-AIC inferred intent only。
- T-AIC SDK/default intent。
- T-AIC explicit hint。
- T-AIC + TSAPS。
- T-AIC + TSAPS + memory hotset。
- T-AIC + TSAPS + memory/rootfs/control hotset。
- Full system with DALC。

### Expected Results

保守目标:

- T-AIC inferred/default intent: `envd_ready -> first_tool_first_byte` 降低 20% 到 30% 以上，不要求用户代码修改。
- T-AIC explicit hint: 在 inferred/default 基础上进一步提升命中率和 tail latency。
- TSAPS: Python/Node first-tool p50 降低 40% 以上，private RSS 降低 20% 到 40%。
- FTMH: 相比 memory-only prefetch，Python/browser workload p95 再降低 20% 以上。
- DALC: 25 到 50 并发 burst 下 resource acquire p99 降低 50% 以上。
- Full system: warm profile + capsule hit 时 create+first-tool first byte p50 进入 50ms 到 100ms 区间；无 capsule 但 profile hit 时 p50 进入 100ms 到 180ms 区间。

## 8. 工业部署路径

部署原则:

- 所有机制 feature-flagged。
- 先 shadow 采集 hotset，不影响线上路径。
- seed 发布必须通过 scrub validator。
- capsule miss 必须无性能负回退。
- 所有 profile seed 都有 scope 和 TTL。

灰度顺序:

1. 只上线 first-tool metrics。
2. 上线 T-AIC inferred/default warmup，但不执行 first action。
3. 对单一 public profile 上线 TSAPS，例如 code-interpreter/python。
4. 对单一 intent 上线 memory hotset。
5. 加 rootfs hotset。
6. 小规模上线 capsule。
7. 最后开放 create-and-run streaming。

失败处理:

- hotset 预取失败: 忽略并走 demand paging。
- capsule reset 失败: 销毁 capsule，不放回池。
- seed taint fail: 不发布 seed。
- first tool fail: 返回 action error，但 sandbox 保留或按请求策略销毁。

## 9. 论文风险和应对

风险一: 创新被认为只是 warm pool 或 prefetch。

应对: 强调 T-AIC 和 first-tool metric 是新的语义边界；TSAPS 解决 Agent 多租户 seed 安全；FTMH 是 intent-keyed multi-modal hotset，不是普通 page prefetch。

风险二: TSAPS 很难证明没有内存泄漏。

应对: 论文中不声称形式化证明。采用系统可检查的 taint policy、实例态注入约束、恢复差分测试和红队检查。Public seed 只允许在用户态 warmup 前构建，Team seed 需要 opt-in。

风险三: Firecracker layered snapshot 和 UFFD 结合复杂。

应对: 初期用现有 template snapshot + shared memfile 验证收益；如果 file backend 全量 load 成本过高，论文把它作为工程限制，核心贡献仍可在 T-AIC/FTMH/DALC 上成立。

风险四: Agent first action 不可预测。

应对: 不假设完全预测。T-AIC 的显式 hint 是 optional；无 hint 时走 inferred/default profile；错误 hint 只影响命中率，不影响正确性。

风险五: 方案透明度不足，需要用户改代码。

应对: 主线设计改为 T-AIC。论文实验必须单独报告 Transparent 和 Annotated 模式，证明不修改用户 Agent 代码也有收益；Explicit hint 只作为性能上界或高级用户选项。

风险六: 工业部署成本太高。

应对: 每个机制都有独立 fallback 和预算控制。DALC 保留启动资源而非完整 warm sandbox，资源占用比 warm pool 更可控。

## 10. 与 V1 设计的区别

V1 是工程设计，提出了 Agent Runtime Interface、profile seed、intent prefetch、launch capsule 和调度方向。

V2 收窄并增强为论文设计:

- 把研究对象明确为 first-tool cold start，而非 sandbox ready。
- 把 AIC 改成 T-AIC，明确透明推断、SDK 内部 hint、模板 hint 和显式 hint 四个兼容级别，区别于 AFaaS 的 FRI。
- 把 profile seed 扩展为 TSAPS，加入 taint gate 和 seed safety。
- 把 prefetch 扩展为 FTMH，覆盖 memory/rootfs/control/network policy，并引入 coverage/waste/deadline。
- 把 resource pooling 扩展为 DALC，强调 atomic resource escrow 和 scheduler-visible inventory。
- 增加 Agent-specific benchmark、消融实验和工业灰度路径。

## 11. 参考资料

- AFaaS: [Fork in the Road: Reflections and Optimizations for Cold Start Latency in Production Serverless Systems](https://www.usenix.org/conference/osdi25/presentation/chai-xiaohu), OSDI 2025.
- Firecracker: [Firecracker: Lightweight Virtualization for Serverless Applications](https://www.usenix.org/system/files/nsdi20-paper-agache.pdf), NSDI 2020.
- Firecracker snapshot docs: [Snapshot Support](https://github.com/firecracker-microvm/firecracker/blob/main/docs/snapshotting/snapshot-support.md).
- OS co-design: [Taming Serverless Cold Starts Through OS Co-Design](https://arxiv.org/html/2509.14292v1), arXiv 2025.
- PASS: [Expeditious High-Concurrency MicroVM SnapStart in Persistent Memory](https://www.usenix.org/system/files/atc24-pang.pdf), ATC 2024.
- Snapshot safety: [Restoring Uniqueness in MicroVM Snapshots](https://ar5iv.labs.arxiv.org/html/2102.12892).
- Kubernetes Agent Sandbox: [kubernetes-sigs/agent-sandbox](https://github.com/kubernetes-sigs/agent-sandbox).

## 12. 一句话论文定位

本文不是又一个 "更快的 microVM restore" 系统，而是提出 **Agent intent-aware sandbox cold start**: 通过把 first tool action 提前暴露给调度器、snapshot builder、prefetcher 和 resource manager，在保持多租户安全的前提下，把 Agent sandbox 的优化目标从 VM/envd ready 推进到 first tool result ready。
