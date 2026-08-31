# Nomad 部署笔记 — api / client-proxy (sh_dev)

节点：`d8b475a4-da45-7475-493d-a1fe8303da30`（dev-server-api，单机 server+client）
配置目录：线上 `/mnt/nfs/dev/2026.22/`，仓库 `volcano_hcls/sh_dev/`

## 一、HCL 改动

### 1. `canary` 必须为 0

`api-pg.hcl` 原来是 `canary = 1`，报错：
```
Dimension "network: reserved port collision api=3000" exhausted on 1 nodes
```
原因：**静态端口 + canary + 单节点约束** 三者互斥。canary 要求同机先起新 alloc 再杀旧的，两者都抢 3000/5009。

```hcl
update {
  canary = 0
  # auto_promote = true    # canary=0 时 Nomad 校验会拒绝此字段，必须一起注掉
}
```

`client-proxy.hcl` 早就是 `canary = 0`（同样用静态端口 3001/3002），api 这份是漏改。

### 3. 强制重新部署：`meta` 块

spec 完全没变时 `nomad job run` 不会新建版本、不会重建 alloc（输出里 `Job Version` 不变、`Evaluation within deployment` 指向旧 deployment）。

```hcl
job "api" {
  meta {
    force_redeploy = "v3"    # 每次递增；job 级 meta 属破坏性变更，会真正替换 alloc
  }
```

镜像用可变 tag（如 `api:dev-test`）时还需 `force_pull = true`，否则本地有缓存不会重拉。

## 二、内存 oversubscription

`memory_max` 默认被忽略，warning：

```
Memory oversubscription is not enabled; Task "api-service.start" memory_max value will be ignored.
```

这是**集群级运行时配置**（存 raft，不在 agent 配置文件里），改完立即生效、不用重启：

```bash
nomad operator scheduler set-config -memory-oversubscription=true
nomad operator scheduler get-config     # 确认
```

**但已运行的 alloc 不会自动获得新上限** —— `AllocatedResources` 在调度那一刻固化：

```bash
nomad alloc status -json 1e7243d3 | python3 -c "
import json,sys
for n,t in json.load(sys.stdin)['AllocatedResources']['Tasks'].items(): print(n, t['Memory'])"
# 生效前: {'MemoryMB': 2048, 'MemoryMaxMB': 0}
# 生效后: {'MemoryMB': 2048, 'MemoryMaxMB': 4096}
```

必须重建 alloc（见下节）。

## 三、线上操作

### 重建 alloc（让集群配置变更生效）

```bash
nomad alloc stop <alloc-id>                  # 最快，不改文件
nomad job restart -reschedule -yes api       # -reschedule 是关键：不加则原地重启容器，不重新调度
```

改 `meta.force_redeploy` + `nomad job run` 也可以，且同时 bump 版本号。

### deployment 卡住时

```bash
nomad deployment fail <deployment-id>
nomad job stop -purge api        # 彻底重来
```

### 反推线上 job spec

alloc 不存 spec，但绑定一个 job version，且 job 版本历史 GC 阈值远长于 alloc：

```bash
nomad job history -p api              # 每版全文
nomad job inspect -version=7 api      # 指定版本 JSON
```

反推的三个坑：
- **等于默认值的字段不落盘**（如 `force_pull = false`），反推不出原本写没写
- **Nomad 自动注入 constraint**：`${attr.os.signals} set_contains SIGTERM`（来自 `kill_signal`）、`${attr.consul.version} semver >= 1.8.0`（来自 consul service）——不是原 HCL 内容，别照抄
- 没有 JSON→HCL 转换器，只能手工转写；验证用 `nomad job run -output x.hcl` 反向对比

## 四、alloc 记录消失的两个原因

历史 alloc「不见了」是两套独立 GC，都是正常行为：

| 现象 | 机制 | 默认阈值 |
|---|---|---|
| `nomad job status` 里查不到 | server 端 eval/alloc GC（raft） | `eval_gc_threshold = 1h`（终态后） |
| `/opt/nomad/data/alloc/` 里没目录 | client 端磁盘压力 GC | `gc_disk_usage_threshold = 80`（磁盘超 80% 触发） |

本机磁盘 83%，所以 client GC 一直在清。**运行中的 alloc 永不被 GC** —— 目录数恒等于 `num_allocations`。

真正的损失是故障 alloc 的日志随目录消失，事后无法 `nomad alloc logs` 回溯。三条对策：

1. 清磁盘降到 80% 以下 —— `docker image prune -a`（`force_pull=false` 会攒镜像）
2. 放宽阈值 `/opt/nomad/config/default.hcl`（**需重启 agent**）：
   ```hcl
   client { gc_disk_usage_threshold = 90, gc_max_allocs = 50 }
   server { eval_gc_threshold = "24h" }
   ```
3. **日志走 Loki（长期正解）** —— 本机已有 logs-collector + loki，不受 alloc GC 影响

## 五、Nomad 三层配置（关键心智模型）

| 层 | 位置 | 生效方式 | 范围 |
|---|---|---|---|
| Agent 配置 | `/opt/nomad/config/default.hcl` | 改文件 + **重启/SIGHUP** | 单节点 |
| 运行时集群配置 | raft（无文件） | CLI/API **立即生效** | 整个集群 |
| Job spec | `*.hcl` | `nomad job run` | 单个 workload |

「内存」一个话题横跨三层：job 写 `memory_max`（三层）→ 需集群开 oversubscription（二层）→ 看节点实际余量。

发现可改项：
```bash
nomad operator scheduler set-config -h    # 二层全部开关
nomad operator                            # 集群级工具总览
nomad job init                            # 带全注释的 job 模板
```

常用二层开关：`-memory-oversubscription`、`-scheduler-algorithm=binpack|spread`、`-preempt-service-scheduler`（抢占只解决 CPU/内存不足，**静态端口冲突属硬约束，抢占救不了**）、`-reject-job-registration`（事故时挡自动化提交）。

并发安全更新用 `-check-index=<Modify Index>` 做 CAS。不带参数跑 `set-config` 是安全的（只更新显式传的 flag），仅 bump Modify Index。

## 六、值得加的习惯

```bash
nomad operator snapshot save backup.snap    # 备份整个 raft 状态，alloc 记录可捞回
nomad operator debug                        # 一键打包完整诊断档
nomad operator api /v1/agent/self           # 带 ACL token 查 API，比 curl 省事
```
