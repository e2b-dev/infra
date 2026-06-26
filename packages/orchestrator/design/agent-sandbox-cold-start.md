# Agent Sandbox 冷启动优化设计

日期: 2026-06-26

## 摘要

本文基于论文 [Fork in the Road: Reflections and Optimizations for Cold Start Latency in Production Serverless Systems](https://www.usenix.org/conference/osdi25/presentation/chai-xiaohu) 及其 [USENIX 论文 PDF](https://www.usenix.org/system/files/osdi25-chai-xiaohu.pdf)，讨论如何在 E2B Firecracker sandbox 上针对 Agent 负载进一步降低端到端冷启动延迟。

论文将端到端冷启动定义为: 节点收到 scheduler/auto-scaler 的扩容请求后，创建实例、加载代码、从 node gateway 获取请求、执行用户代码，并把响应返回给终端用户的完整过程。对 Agent sandbox 来说，等价口径不应止步于 "sandbox 已创建" 或 "envd 可访问"，而应到首次 Agent 工具动作产生可用响应为止，例如第一次 command stdout、Python kernel ready、文件操作返回、浏览器/MCP 工具返回。

AFaaS 已经把通用 serverless 冷启动拆成控制路径、资源争用、用户代码初始化三类，并分别用 FRI、资源池化/共享、树状 seed 优化。Agent sandbox 还有额外空间，核心原因是 Agent 负载比通用 FaaS 更可预测: runtime 和工具链高度重复，首次动作通常可由 SDK/API 暴露，会话具有连续性，同一团队或同一 Agent profile 会形成短时突发。

本文建议把现有 E2B 的 snapshot resume、template cache、network/NBD pool、UFFD prefetch、layered snapshot、shared memfile、CoW overlay 演进成 Agent 专用的四个机制:

1. Agent Runtime Interface: 把 create、init、route、first action 融合为单次控制路径。
2. Agent profile seed tree: L0 基础层、L1 runtime/tool 层、L2 team/template 层、L3 session 层的多级 snapshot。
3. Intent-driven prefetch: 基于首次工具动作轨迹训练内存/rootfs/网络热路径，而不是只训练 envd init。
4. Launch capsule pool: 把 network slot、NBD/rootfs overlay、cgroup、sandbox files、Firecracker socket 等启动资源作为一个可调度的原子胶囊预留。

目标是在不牺牲隔离和数据安全的前提下，把当前真实 Firecracker resume 的 300ms 级 ready 延迟逐步压到 150ms 内，并在命中 Agent profile seed 与 launch capsule 的情况下，把 create+first action 的 p50 推向 50ms 到 100ms 区间。

## 论文结论

论文指出，生产 serverless 的冷启动瓶颈不只在容器或 microVM 初始化。AFaaS 观测到三类被忽略的端到端延迟:

控制路径延迟: 当 fork/restore 本身已经足够快后，OCI/containerd/runc/shim/binary/RPC 之间的多层调用会变成主要成本。论文测得控制路径约 18ms 到 25ms，在 Catalyzer 环境中占冷启动的 30% 到 40%。AFaaS 用 FRI 替代通用 OCI 的部分 shim-call，把 serverless 启动路径改成更短的 runtime 内接口。

资源争用延迟: 高并发启动时，namespace、veth、cgroup、network stack、seccomp 编译/安装等低层资源创建会争用内核锁和 CPU，导致 tail latency 和持续吞吐下降。AFaaS 的策略是能继承的放进 seed，能预分配的做 pool，例如 veth pool、cgroup pool、预编译 seccomp。

用户代码初始化延迟: language runtime、JIT、依赖加载经常超过函数真实执行时间。AFaaS 把用户代码也纳入 seed，并用树状 seed 加 CoW 共享内存来避免每个用户代码 seed 都占用完整内存。

论文的关键启发不是某个单点技巧，而是 E2E 口径和 "就近可用祖先 seed" 的思路: 如果用户代码级 seed 不可用，仍可从语言 runtime seed 或基础 seed 启动，保持 best-effort 的性能退化。

## 当前 E2B 路径映射

E2B 当前是 Firecracker microVM sandbox，常见线上路径是 API 创建 sandbox，API 选择节点后通过 orchestrator gRPC 调用 `Create`，orchestrator 拉取 template/snapshot 并 `ResumeSandbox`，再等待 envd init。

关键路径在这些文件中:

- API 创建入口: `packages/api/internal/handlers/sandbox_create.go`
- API 到节点的 gRPC 创建: `packages/api/internal/orchestrator/nodemanager/sandbox_create.go`
- orchestrator 创建入口: `packages/orchestrator/pkg/server/sandboxes.go`
- sandbox create/resume 主逻辑: `packages/orchestrator/pkg/sandbox/sandbox.go`
- Firecracker resume/load snapshot: `packages/orchestrator/pkg/sandbox/fc/process.go`
- envd init polling: `packages/orchestrator/pkg/sandbox/envd.go`
- template cache: `packages/orchestrator/pkg/sandbox/template/cache.go`
- network slot pool: `packages/orchestrator/pkg/sandbox/network/pool.go`
- NBD device pool: `packages/orchestrator/pkg/sandbox/nbd/pool.go`

现状中已有若干可复用基础:

- `template.Cache` 缓存 template、metadata、snapshot/rootfs/memfile。
- `network.Pool` 预创建和复用 network slot。
- `nbd.DevicePool` 预留 NBD 设备号。
- `ResumeSandbox` 已并行初始化 UFFD、network slot、rootfs overlay。
- `prefetch_harvest` 和 build optimize phase 已能收集 resume prefetch mapping。
- `metadata.LayeredSnapshot`、`template.LayeredTemplate`、`SharedMemfileManager`、`CoWOverlay` 已经给多层 snapshot 和共享内存留下了落点。
- `AgentPriorityBuilder` 已经表达了 Agent runtime 页面的优先级雏形。

已有 benchmark `packages/orchestrator/benchmarks/e2b_real_firecracker_benchmark.md` 显示，真实 Firecracker snapshot resume 在 1 并发下平均约 315ms，10 并发下平均约 369ms。并发扩展比对照系统更稳定，但单次 E2E ready 仍远高于 AFaaS 的毫秒级目标。这个差距不能只靠更多缓存解决，需要把 Agent 的 workload contract 引入启动路径。

## Agent 负载特征

Agent sandbox 不等同于通用 FaaS 函数。

首先，Agent 是会话型负载。一次 sandbox 创建后，通常会执行一串命令、文件操作、代码解释、浏览器或 MCP 工具调用。冷启动最影响的是第一步工具动作，而不是后续长时间运行。

其次，Agent runtime 高度重复。大量请求共享 Python、Node.js、uv/pip/npm、Jupyter/kernel、browser runtime、repo tooling、MCP bridge、envd/proxy/gateway 等相同基础。通用 FaaS 的 "user code seed" 命中率可能受函数长尾影响，但 Agent 的 "tool profile seed" 可以跨任务和跨用户复用更多状态。

第三，首次动作通常可预测。SDK 创建 sandbox 后经常立即执行 `commands.run`、`runCode`、文件写入或启动一个工具 server。相比通用 HTTP 函数，Agent 平台有机会在创建请求里携带 first action intent。

第四，Agent 会形成 burst。一次用户会话、一次 benchmark、一次并行代码任务会在短时间内创建多个同 profile sandbox，且往往来自同团队、同模板、同 region。调度器可以利用这种局部性把请求放到 profile seed 和热页已经存在的节点。

最后，Agent 对隔离更敏感。sandbox 里会出现 prompt、repo、secrets、tool credentials、临时文件和命令历史。可共享 seed 必须明确排除这些实例数据。

## 设计目标

P0 目标: 建立真实 E2E 冷启动指标，覆盖 API 入站、节点选择、orchestrator 创建、envd init、首次工具动作返回。当前 `orchestrator.sandbox.create.duration` 和 `orchestrator.sandbox.envd.init.duration` 不足以衡量 Agent 冷启动。

P1 目标: 在现有 snapshot resume 路径上，把 warm node、template cache hit、network/NBD pool hit 的 create+first command p50 降到 150ms 内，p95 降到 250ms 内。

P2 目标: 在 Agent profile seed 和 launch capsule 命中时，把 create+first action p50 降到 50ms 到 100ms 区间，并保持 10 到 50 并发下 tail latency 不随并发线性恶化。

P3 目标: 在 memory/rootfs hotset 可共享时，降低每个活跃 sandbox 的增量内存占用，使高并发 Agent burst 不因为 seed 数量膨胀而抵消冷启动收益。

非目标:

- 不以牺牲 microVM 隔离为代价换取启动速度。
- 不把用户 prompt、workspace、secrets、access token、SSH key、浏览器 profile、命令历史写入跨租户共享 seed。
- 不用常驻热 sandbox 伪装成冷启动优化。热池可以作为 fallback，但本文重点是 cold path 本身。
- 不把所有功能一次性落地到生产。需要 shadow mode 和 feature flag 分阶段验证。

## 方案一: Agent Runtime Interface

AFaaS 的 FRI 缩短的是高低层 runtime 之间的容器控制路径。E2B 对 Agent 还可以进一步缩短 "创建 sandbox 后立刻执行首次工具动作" 的平台控制路径。

当前典型路径是:

1. SDK 调 API 创建 sandbox。
2. API 解析模板、鉴权、建记录、调度节点。
3. API 调 orchestrator `Create`。
4. orchestrator resume VM 并等待 envd `/init` 成功。
5. API 返回 sandbox ID。
6. SDK 再通过 client-proxy/envd 发送第一条 command 或 kernel 请求。

这条路径对通用 sandbox API 合理，但如果用户真实意图是 "创建后马上运行代码/命令"，步骤 5 和 6 是额外控制路径。Agent Runtime Interface, 简称 ARI, 建议把 first action intent 带进创建请求，使 orchestrator 可以在 envd 初始化完成的同一时刻把首个动作交给 envd，或者至少在返回 API 前完成 tool gateway 的预绑定。

建议新增两类能力:

`CreateWithIntent`: API/SDK 可选传入 `agent_profile_id`、`first_action_type`、`first_action_shape`、`expected_ports`、`expected_runtime`。first action payload 可以先只支持无副作用或低风险动作，例如 "start python kernel"、"run command with argv"、"ensure tool server ready"。不需要替代现有 `Create`。

`InitAndFirstAction`: envd 增加一个幂等接口或内部队列，允许 orchestrator 在 `/init` 成功后立即提交第一动作，返回 action accepted/first byte/complete 三种状态。对于 `commands.run`，E2E 可以定义到 first stdout 或 process started；对于 `runCode`，定义到 kernel ready 或首个 execution response。

ARI 的收益来自三点:

- 消除 SDK/API/proxy 的第二个请求往返和路由发现延迟。
- 让调度器和 orchestrator 更早知道需要哪个 profile seed 和哪些资源。
- 把 envd init 和 tool runtime ready 变成同一个被观测的 E2E span，而不是两个孤立动作。

短期可落地的最小版本:

- 在 API create 请求中增加实验性 `agentIntent` 字段，仅 feature flag 开启。
- orchestrator `Create` 响应前不执行用户命令，只提前启动 tool gateway/kernel，并把 `first_action_ready_at` 写入 trace。
- SDK 仍发第二个命令请求，但命中已经启动的 gateway/kernel。

长期版本:

- 增加 `CreateAndRun` 或 `CreateAndConnect` API，返回 sandbox ID 加 first action result stream。
- client-proxy 支持在 sandbox 尚未 fully registered 时接受 pending route，等 orchestrator 标记 routable 后无缝转发。

安全边界:

- first action 不进入共享 seed。
- access token、env vars、volume mounts 仍按实例注入。
- 如果 first action 失败，sandbox 是否保留由请求参数决定，默认保留以便调试。

## 方案二: Agent Profile Seed Tree

AFaaS 的树状 seed 以函数和语言 runtime 为层级。Agent sandbox 更适合以 "工具 profile" 建树，因为 profile 比单个用户代码更稳定，且可跨更多请求复用。

建议层级:

L0 infrastructure seed: kernel、init、systemd/envd、基础网络、证书、日志代理、基础目录结构。跨所有 sandbox 共享，不包含语言 runtime 和用户数据。

L1 runtime seed: Python/Node.js/uv/pip/npm、Jupyter/kernel runner、常用系统库、常用 package metadata、MCP/tool bridge、E2B SDK runtime support。按架构、内核版本、Firecracker 版本、envd 版本、runtime 版本区分。跨团队共享，但必须不包含租户数据。

L2 agent profile seed: code-interpreter、browser-agent、repo-agent、data-analysis 等 profile。包含常用工具进程的 warmed-but-idle 状态，例如 Python interpreter 已启动、Node gateway 已加载、Playwright 浏览器依赖已 mmap、常用 imports 已编译。可以跨团队共享，但只允许放公共包和公共工具状态。

L3 team/template seed: 某团队自定义模板依赖、私有但同团队可共享的 package cache、已安装 repo-independent toolchain。只在同团队/同模板内复用。默认 opt-in，且需要 scrub validator。

L4 session checkpoint: 单个 sandbox pause/auto-pause 后的 continuation。包含用户 workspace 和运行态，只能由同 sandbox/session 恢复，不参与共享。

这棵树和当前仓库的映射:

- `metadata.LayeredSnapshot` 可扩展为携带 `agent_profile_id`、`seed_scope`、`scrub_manifest`、`warmup_manifest`。
- `LayeredTemplate` 当前有 L0/L1/L2 结构，可以把 L1 明确为 runtime/profile seed，把实例私有层作为 L3/L4 delta。
- `SharedMemfileManager` 负责 L0/L1/L2 shared memfile map 和 page cache 复用。
- `CoWOverlay` 和 exit reclaim 可用于 session delta 的保存和回收。
- build finalize phase 已运行 start/ready command，可扩展 profile warmup command，在 snapshot 前确认 tool gateway idle。

Profile seed 的 build 流程:

1. 构建 L0: 只跑基础系统和 envd ready。
2. 构建 L1: 从 L0 启动，安装语言/runtime/tool bridge，运行 runtime warmup，例如 import 常用模块、启动 kernel skeleton、加载 Node gateway。
3. 构建 L2: 从 L1 启动，按 agent profile 运行 warmup，例如 code-interpreter 预热 Python kernel，browser profile 预热 browser dependencies，repo profile 预热 git/language server。
4. scrub: 删除实例标识、token、history、tmp、workspace、SSH material、browser user data、随机种子文件、network connection state。
5. snapshot: 保存 memfile/rootfs/snapfile 和 prefetch mapping。
6. validate: 从 seed 恢复两次，比较 scrub 后的 filesystem/memory metadata，确保没有跨实例标识泄漏。

为什么这是 Agent 专用增量:

通用 FaaS 的 user code seed 容易被长尾函数稀释，且每个函数都占内存。Agent profile seed 命中率更高，因为大量用户共享同一工具栈。它不是为每个用户 prompt 或 repo 做 seed，而是为 "代码解释器 profile"、"浏览器 profile"、"repo 工具 profile" 做 seed。

## 方案三: Intent-Driven Prefetch

当前 optimize phase 和 pause-resume prefetch 主要捕捉 envd init 或 resume working set。对 Agent E2E 冷启动，更关键的是首次工具动作 hotset。

建议把 prefetch mapping 从 "resume 到 envd ready" 扩展到 "resume 到 first action response"。不同 first action 产生不同 hotset，不能混在一个无优先级列表里。

新增 profile:

- `envd_ready`: 当前口径，P0。
- `command_start`: envd init 加 process spawn、shell/exec、pty/log stream。
- `python_kernel_ready`: Python runtime、site-packages metadata、kernel transport、常用 imports。
- `node_gateway_ready`: Node runtime、V8 code space、gateway modules、JSON/RPC path。
- `browser_ready`: browser binary mmap、font/cache、sandbox flags、IPC socket。
- `mcp_bridge_ready`: MCP server process、stdio/http bridge、tool schema load。

每个 profile 生成三类热路径:

memory hotset: UFFD page fault trace，按首次访问顺序和动作阶段记录。现有 `MemoryPrefetchMapping` 可扩展 `phase` 和 `priority`。

rootfs hotset: NBD/block layer 记录 first action 前读过的 inode/block，启动前异步拉取或将高频 metadata block 常驻 shared chunk cache。

network hotset: 不把真实连接放进 seed，而是预编译/预装 firewall、egress policy、DNS allowlist、proxy route。对于固定公共 endpoint，可在 host 侧维护 DNS cache，但不共享认证连接。

优先级:

P0: envd `/init`、日志、control channel、必要 kernel/envd 页面。

P1: first action 的进程启动和 IPC 路径，例如 shell、Python kernel control socket、Node gateway handler。

P2: 常用 import/package metadata、tool schema、runtime heap。

P3: 长尾库和非关键 cache。

现有 `AgentPriorityBuilder` 当前使用固定 GPA range，这适合原型但不适合生产。生产版本应使用训练结果生成 region 或 block index，而不是硬编码 0 到 230MiB。可以保留 builder 作为 fallback，但更好的数据来源是 build optimize phase 与 shadow traffic 的 page-fault attribution。

训练方式:

- build-time: 在 `packages/orchestrator/pkg/template/build/phases/optimize` 中增加 `agent_profile_warmups`，每个 warmup 执行指定 first action 并收集 prefetch data。
- runtime shadow: 对真实 sandbox 的前 N 秒 page fault/rootfs read 做采样，只上传去标识化 block indices 和 action type，不上传用户路径内容。
- merge: 只保留多次运行交集或高置信 block，避免把用户特定路径写入共享 profile。

线上执行:

- scheduler 在节点选择前读取 `agent_profile_id` 和 first action，优先选择有对应 hotset 的节点。
- orchestrator 在 `templateCache.GetTemplate` 之后、等待 network slot/NBD/cgroup 时同步启动 memory/rootfs prefetch。
- launch capsule 命中时，prefetch 应已在 capsule 准备阶段完成，不占用户 critical path。

## 方案四: Launch Capsule Pool

AFaaS 对 veth、cgroup 等资源分别池化。E2B 已有 network pool 和 NBD device pool，但 Create/Resume 仍在请求路径里组合多个资源:

- sandbox cache dirs/files
- network slot 配置与 egress policy
- rootfs overlay/NBD provider
- UFFD socket/memory backend
- cgroup 创建和 FD 获取
- Firecracker socket/metrics FIFO
- rootfs symlink

这些资源分别初始化会造成两个问题: 单个池命中不代表整体可启动，且高并发时每个请求都在临界路径里争抢多个锁和内核对象。Agent burst 场景下，可以把它们预组装成 "launch capsule"。

Capsule 内容:

- 一个已分配但未绑定用户数据的 network slot，附带可快速切换的 egress policy handle。
- 一个 NBD slot 和 rootfs overlay skeleton，绑定到某个 template/profile 的 readonly rootfs，writable layer 为空。
- 一个预创建 cgroup 目录和打开的 cgroup FD。
- sandbox files 目录、Firecracker API socket path、metrics FIFO path。
- 可选: 已启动但未加载 snapshot 的 Firecracker API process。此项风险较高，需要验证 Firecracker 进程是否能在不携带用户态状态的情况下安全复用。

Capsule key:

`{cluster, arch, kernel_version, firecracker_version, template_build_id, agent_profile_id, network_policy_shape, memory_mb, vcpu}`

Pool 策略:

- 每个 hot profile 保持小规模 ready capsule，例如 1 到 4 个，按近期 burst 预测动态扩缩。
- capsule 创建在低优先级 preparation lane 中串行或限速执行，避免论文提到的 "preparation 成为 noisy neighbor"。
- memory pressure、NBD pressure、network slot pressure 超过阈值时自动降级，只保留 L0/L1 热页，不保留完整 capsule。
- capsule TTL 短，例如 30 到 120 秒；命中 burst，过期即回收。

和现有代码的关系:

- `network.Pool` 保留，但 capsule 从 pool 中借出 slot 后提前配置静态部分。
- `nbd.DevicePool` 保留，但新增 rootfs overlay capsule，避免在用户路径里完整创建 overlay。
- `cgroup.Manager` 需要增加 cgroup pool，接近论文 AFaaS 的 cgroup pool。
- `sandbox.Factory.ResumeSandbox` 增加可选 `LaunchResources` 参数，命中 capsule 时跳过对应初始化。

风险:

- rootfs writable layer 必须确保空白且不可被前一个 sandbox 污染。
- network slot 复用必须继续保留 drain delay，防止旧连接流入新 sandbox。
- cgroup FD 预创建不能绕过 per-sandbox resource accounting。
- Firecracker pre-spawn 如果引入复杂生命周期，先不进入 P1。

## 方案五: 调度器的 Profile Locality

冷启动优化不能只在单节点做。Agent profile seed、prefetch hotset 和 capsule 都有节点局部性。如果调度器把请求放到没有 profile cache 的节点，所有优化都会退化为远端拉取和临时准备。

建议 NodeManager 汇报以下扩展状态:

- 已缓存 template build IDs。
- 已缓存 agent profile seed IDs。
- L0/L1 shared memfile resident bytes 和热页估计。
- ready capsule count by capsule key。
- NBD/network/cgroup pool pressure。
- 最近 1 分钟 profile 命中率和创建 p95。

Placement scoring:

`score = capacity_score + cache_locality_score + capsule_score - pressure_penalty - cross_node_fetch_penalty`

对于同一 team/session 的连续 sandbox，优先同节点或同机架，以复用 page cache 和 template rootfs cache。对于高并发 fan-out，不要把所有请求压到一个热节点，需要在 "局部性" 和 "并发资源压力" 之间做权衡。

仓库已有 `SchedulingMetadata` 和 label-based scheduling feature flag，可作为扩展入口。初期可以只在 API 层做 soft preference: 若多个节点容量相近，选择有 `agent_profile_id` 命中的节点。

## 端到端指标

需要新增 Agent E2E span，而不是只看 sandbox create。

建议事件点:

- `api.sandbox.create.received`
- `api.template.resolved`
- `api.node.selected`
- `orchestrator.create.received`
- `template.cache.ready`
- `launch_capsule.acquire.start/end`
- `network.ready`
- `rootfs.ready`
- `memory_backend.ready`
- `cgroup.ready`
- `fc.resume.start/end`
- `envd.init.start/end`
- `first_action.accepted`
- `first_action.first_byte`
- `first_action.complete`

核心指标:

- `agent.sandbox.cold_start.e2e.duration`: API 入站到 first action first byte/complete。
- `agent.sandbox.create_to_envd.duration`: API 入站到 envd init success。
- `agent.sandbox.envd_to_first_action.duration`: envd ready 到 first action。
- `agent.sandbox.profile_seed.hit`: L0/L1/L2/L3 命中层级。
- `agent.sandbox.launch_capsule.hit`: capsule hit/miss/cold-created。
- `agent.sandbox.prefetch.coverage`: first action demand faults 中被 prefetch 覆盖比例。
- `agent.sandbox.prefetch.waste`: prefetch 后 first action 未使用的页/块比例。
- `agent.sandbox.resource_pool.wait`: network/NBD/cgroup/capsule wait。
- `agent.sandbox.memory.incremental_rss`: 每 sandbox 增量 RSS，区分 shared/private。

Benchmark 矩阵:

- concurrency: 1、5、10、25、50。
- node state: cold node、warm template、warm profile、warm capsule。
- action: command_start、python_kernel_ready、node_gateway_ready、browser_ready、mcp_bridge_ready。
- template: base、code-interpreter、large custom dependency、filesystem-only snapshot、memory snapshot。
- network: no egress、default egress、domain allowlist、BYOP proxy。

成功标准:

- P1: warm profile node 的 create+first command p50 < 150ms，p95 < 250ms。
- P1: 10 并发下 p95 不超过 1 并发 p95 的 1.5 倍。
- P2: capsule hit 的 create+first action p50 < 100ms。
- P2: profile seed shared pages 使 per-sandbox private RSS 相比普通 snapshot resume 下降 30% 以上。
- P2: prefetch waste 在 P0/P1 priority 不超过 20%。

## 安全和正确性

共享 seed 必须遵守作用域:

- Global scope: 只允许公共 runtime、公共工具、无租户数据。
- Team scope: 可包含团队私有依赖，但只能同团队复用。
- Session scope: 可包含 workspace、prompt、secrets、命令历史，只能同 sandbox/session 恢复。

scrub validator 必须检查:

- `/home/user/workspace` 是否为空或在 allowlist 内。
- shell history、Python history、Jupyter runtime、browser profile、SSH/Git credentials 是否为空。
- env vars、access token、MMDS metadata、logs collector token 是否未固化。
- `/tmp`、`/run`、socket、pidfile、lockfile 是否不会跨实例复用。
- DNS cache 或 package cache 不包含认证 header 或私有 registry token。
- random seed、machine-id、hostname、sandbox ID 是否会在 resume/init 时重置。

运行时隔离:

- 每次 resume 后仍必须执行 envd init 或等价实例化步骤，写入新的 sandbox ID、access token hash、mounts、CA bundle、proxy config。
- network slot 复用必须关闭旧连接并重置 firewall/nat/egress policy。
- cgroup 复用必须确认无残留进程，memory.peak 等统计语义不混淆。
- first action fusion 不能绕过鉴权、quota、audit event 和 sandbox lifecycle event。

## 分阶段实施

### Phase 0: 指标和基线

目标是先避免优化错口径。

工作项:

- 在 API、orchestrator、envd/client-proxy 增加 Agent E2E trace events。
- 增加 benchmark: create+first command、create+python kernel ready、create+first file op。
- 把 `packages/orchestrator/benchmarks/e2b_real_firecracker_benchmark.md` 的 ready benchmark 扩展到 first action。
- 拆分 `orchestrator.sandbox.create.duration`，至少输出 template/cache、resource acquire、fc resume、envd init。

退出标准:

- 能稳定回答 300ms 中 API、scheduler、template、NBD/rootfs、network、cgroup、FC、envd、first action 各占多少。

### Phase 1: Intent 和轻量预热

目标是不改 snapshot 格式，先减少首次动作等待。

工作项:

- API 增加实验性 `agentIntent`。
- SDK 可选传 `first_action_type`。
- orchestrator 创建时根据 intent 提前启动 tool gateway/kernel warmup，但不执行用户命令。
- `OptimizeBuilder` 增加 first action warmup profiles，输出 action-specific prefetch metadata。
- placement soft preference: 优先有 template/profile cache 的节点。

退出标准:

- `envd_to_first_action.duration` 明显下降。
- 没有新增跨租户状态泄漏风险。

### Phase 2: Launch Capsule

目标是减少资源争用和多资源组合等待。

工作项:

- 增加 cgroup pool。
- 定义 `LaunchCapsule` interface，先包含 sandbox files、network slot、NBD slot/rootfs overlay、cgroup。
- `ResumeSandbox` 支持传入 capsule。
- capsule pool 按 profile key 小规模预留，准备过程限速。
- 增加 capsule hit/miss/eviction/contamination-check metrics。

退出标准:

- 高并发下 resource acquire p95 明显下降。
- capsule miss 时性能不回退到比当前更差。

### Phase 3: Profile Seed Tree

目标是把 Agent runtime/tool 初始化从用户路径移到可共享 seed。

工作项:

- 扩展 metadata: `agent_profile_id`、`seed_scope`、`warmup_manifest`、`scrub_manifest`。
- 用现有 `LayeredTemplate` 和 `SharedMemfileManager` 表达 L0/L1/L2 profile seed。
- build pipeline 增加 L1/L2 profile warmup 和 scrub validator。
- 将固定 `AgentPriorityBuilder` 替换为训练生成的 priority hotset。
- profile seed 命中时用 layered snapshot resume，miss 时 fallback 到普通 template snapshot。

退出标准:

- code-interpreter profile 命中时 create+python kernel ready p50 < 100ms。
- shared memfile 降低 per-sandbox private RSS。
- scrub validator 可阻断含敏感实例状态的 seed 发布。

### Phase 4: CreateAndRun 和 Streaming First Result

目标是完整优化 Agent E2E，而不是只优化 ready。

工作项:

- 新增 `CreateAndRun` 或等价 streaming API。
- client-proxy 支持 pending sandbox route。
- envd 支持 init 后立即执行 first action，并返回 first byte/complete。
- lifecycle event 记录 create 和 first action 的关联 execution ID。

退出标准:

- SDK 端 create+first command 只需要一次用户可见请求。
- 错误语义清晰: create failed、init failed、first action failed 可区分。

## 风险

Profile seed 泄漏用户数据: 这是最大风险。必须先做 scrub validator 和 scope enforcement，再做跨团队共享。

Prefetch 过量导致反效果: P0/P1 hotset 必须小，且以 coverage/waste 指标约束。低置信热页只能后台预取，不能阻塞用户路径。

Capsule 污染: network/rootfs/cgroup 复用必须有强 reset 和断言。任何 reset 失败都应销毁 capsule，而不是放回池。

调度过度偏向热节点: locality scoring 需要压力惩罚，避免热节点成为新瓶颈。

Firecracker pre-spawn 复杂度: 预启动 FC API process 有潜在生命周期和隔离风险，放在后期研究，不作为早期依赖。

## 开放问题

E2E first action 的默认口径需要产品和 SDK 一起确定。不同 Agent 场景可能选择 first byte、process started、kernel ready 或 command complete。

Agent profile taxonomy 需要从真实流量里提取。初期可以手工定义 code-interpreter、node-gateway、browser、repo 四类。

Layered snapshot 当前实现把 layered memory 合成单个 memfile 给 Firecracker file backend，是否能和 UFFD demand paging 结合，需要单独评估。若 file backend 全量载入成本过高，可能需要把 profile seed 共享和 UFFD backend 结合。

rootfs hotset 的采集粒度需要确认。NBD block 级映射易落地，但 inode/package metadata 级别更容易解释和调试。

profile seed 的 TTL 和内存预算需要线上数据驱动，不能按固定数量保留。

## 结论

AFaaS 的三类优化仍然成立，但 Agent sandbox 可以比通用 serverless 更进一步。关键不是简单增加热池，而是把 Agent 负载的可预测性显式暴露给 sandbox 平台: 创建时携带 intent，调度时选择 profile-local 节点，启动时命中 launch capsule，恢复时从最近的 profile seed 分支启动，并用首次工具动作训练 prefetch。

这个方向和当前 E2B 代码基础兼容。短期应先做 E2E 指标、intent warmup 和 action-specific prefetch；中期做 launch capsule 和 profile seed；长期再做 create+first-action streaming API。这样可以在保持 Firecracker 隔离的同时，把 Agent sandbox 冷启动从 "VM ready" 优化推进到真正的 "Agent first action ready"。
