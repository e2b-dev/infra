# 火山 vePFS 沙箱共享持久卷 —— 操作笔记

> **目标**：让所有 orchestrator 节点上的 E2B 沙箱共享同一份火山 vePFS（并行文件系统），实现持久卷跨节点共享，并与 NAS 方案做对比。
> **环境**：prod（sh_prod）　**最后验证通过**：2026-08-19（跨节点共享已验证）

---

## 概述：原理与整体流程

一句话原理：`PERSISTENT_VOLUME_MOUNTS = "类型名:根路径"`，卷数据落在 `{根路径}/team-{id}/vol-{id}`。
**只要每台节点把这个根路径挂到同一个火山 vePFS，跨节点就共享了。**

vePFS 通过 **NFS 协议服务**对外暴露，客户端用**标准 NFSv3** 挂载，用法和 NAS 几乎一致（见文末「与 NAS 的差异」）。

整体三步：

```
① 火山云                        ② 每台 Linux 节点               ③ 改配置 + 验证
─────────────────      ──────────────────      ──────────────────
创建 vePFS 文件系统         挂载 NFS 到 /mnt/volcano-vepfs  ──▶  改 HCL → 部署 → 验证共享
创建 NFS 协议服务 + 导出目录    配 fstab 开机自动挂载
```

---

## ① 火山云控制台操作

路径：火山引擎控制台 → **文件存储 vePFS** → 文件系统列表。

### 1.1 创建文件系统
- 类型：按业务选（本项目：**100MB/s/TiB 双端兼容型**）。
- 地域/可用区：华东2（上海）· 可用区A。
- VPC/子网：`VPC-e2b` / `Subnet-clickhouse`（**必须与 orchestrator 节点同一 VPC**）。

> 本项目实际值：文件系统 `sh-prod-e2b-vePFS`（ID `vepfs-cnsh5f1f54cd1ce6`），容量 6TiB。

### 1.2 创建 NFS 协议服务
进入文件系统详情 → **协议服务** 页签 → **创建协议服务**：
- 协议类型：**NFS**（仅支持 NFSv3，处于邀测状态，如不可用需提工单开通）。
- 规格 / 带宽：按需（本项目缓存1型、8000MB/s）。

> 本项目实际值：协议服务 `sh-prod-e2b-vePFS-nfs`（ID `ptc-b8dd59cd`）。
> ⚠️ 单个文件系统只能建 **1 个**协议服务、**1 个**导出目录、导出 **1 个** VPC。

### 1.3 创建导出目录（关键，挂载地址在这里生成）
在协议服务操作列点 **导出目录** → **创建导出目录**：
- 文件系统路径：目前仅支持根目录 `/`。
- 私有网络 / 子网：必须与文件系统同 VPC（`VPC-e2b` / `Subnet-clickhouse`）。
- 权限组：默认（全部允许）。

创建成功后，vePFS 自动生成**挂载地址 + 挂载命令**，直接复制。

> 本项目实际值：导出目录 `volcano-nfs`（ID `export-42c8488c`）
> 挂载地址：`export-42c8488c.cnsh82882aec3efc.3i5xx2ldhtvk05r0lrq5fr9pd.vepfs.ivolces.com:/vepfs`
> ⚠️ 地址前缀带**导出目录 ID**（`export-42c8488c.`），这点和 NAS 直接用域名不同。

---

## ② Linux 服务器操作（每台节点）

> ⚠️ **每台 orchestrator 节点都要做，一台都不能漏。**
> prod 当前已挂节点：`sh-prod-sandbox-node`、`sh-prod-server-api`（API 节点也挂了，方便在宿主机直接查数据）。

### 2.1 安装客户端并挂载
```bash
# 1) 安装 NFS 客户端（和 NAS 相同，系统自带）
apt-get install -y nfs-common

# 2) 建本地挂载目录（与 NAS 的 /mnt/volcano-nfs 区分开）
mkdir -p /mnt/volcano-vepfs

# 3) 挂载（用导出目录页复制的 v3 命令，末尾补本地目录）
#    注意：vePFS 的 NFS 挂载参数比 NAS 少一个 sec=sys
mount -t nfs -o vers=3,nolock,proto=tcp,rsize=1048576,wsize=1048576,hard,timeo=600,retrans=2,noresvport \
  export-42c8488c.cnsh82882aec3efc.3i5xx2ldhtvk05r0lrq5fr9pd.vepfs.ivolces.com:/vepfs /mnt/volcano-vepfs

# 4) 建 volumes 根目录（orchestrator 启动会校验它存在；只建这一个，卷子目录自动生成）
mkdir -p /mnt/volcano-vepfs/volumes

# 5) 确认是真 NFS（关键！必须 nfs，不能 ext2/3/4）
stat -f -c %T /mnt/volcano-vepfs        # 期望输出: nfs
df -h | grep vepfs                       # 期望: vepfs 域名 + 6.0T 容量
mount | grep vepfs                       # 期望: vepfs 域名 + type nfs
```

### 2.2 配置开机自动挂载
`/etc/fstab` 追加（`_netdev` 保证网络就绪后再挂，防重启后 job 起不来）：
```
export-42c8488c.cnsh82882aec3efc.3i5xx2ldhtvk05r0lrq5fr9pd.vepfs.ivolces.com:/vepfs  /mnt/volcano-vepfs  nfs  vers=3,nolock,proto=tcp,rsize=1048576,wsize=1048576,hard,timeo=600,retrans=2,noresvport,_netdev  0  0
```

### 2.3 改 HCL 配置并部署
prod 环境 job 文件的 `PERSISTENT_VOLUME_MOUNTS` 增加 `vepfs` 类型（保留 `local`、`nas`，三类型并存便于对比）：
- `volcano_hcls/sh_prod/orchestrator-with-template.hcl`

```hcl
PERSISTENT_VOLUME_MOUNTS = "local:/data1/orchestrator/volumes,nas:/mnt/volcano-nfs/volumes,vepfs:/mnt/volcano-vepfs/volumes"
```

> ⚠️ HCL 里的路径 = 服务器上 `mount` 的挂载点 + `/volumes`，**必须字面一致**（含 `/mnt` 层级）。
> `ORCHESTRATOR_BASE_PATH` 等本地工作目录保持不动，**只有 volumes 走 vePFS**。

**让新建的卷落到 vePFS**：改 api 的默认卷类型（`volcano_hcls/sh_prod/api.hcl`）：
```hcl
DEFAULT_PERSISTENT_VOLUME_TYPE = "vepfs"
```
> `local`/`nas`/`vepfs` 只是**类型名（标签）**，必须和 orchestrator `PERSISTENT_VOLUME_MOUNTS` 左边的 key 一致。
> sh_prod 的 `LAUNCH_DARKLY_ENABLE=false`，feature flag 不可用，切换卷类型只能改此 env + 重启 api。
> 已建卷的 type 已存进 DB，改这里只影响**之后新建**的卷；旧卷照常工作（前提 orchestrator 有对应类型挂载）。

改完重新部署 orchestrator + api job，确认 running、无 `failed to access persistent volume mount` 报错。

---

## ③ 验证步骤与代码

### 3.1 验证（由浅入深，做到跨节点这步基本就够）

**第一层 · 节点层：每台都是「真 NFS 且同源」**
在**每台** orchestrator 节点执行：
```bash
stat -f -c %T /mnt/volcano-vepfs     # 都必须是 nfs
mount | grep vepfs                    # 源都必须是同一个 vepfs 导出地址
```

**第二层 · 跨节点层：一台写、另一台读**
```bash
# 节点A 写
echo "hello-vepfs-$(date)" > /mnt/volcano-vepfs/probe.txt
# 节点B 读（读到即两台挂的是同一份盘）
cat /mnt/volcano-vepfs/probe.txt      # 期望: hello-vepfs-...
```
> ✅ 已验证（2026-08-19）：`sh-prod-server-api` 写 `probe.txt`，`sh-prod-sandbox-node` 读到同一内容，跨节点共享成立。
> 说明：vePFS NFS **无 root_squash 写限制**，可直接在挂载根目录写探针（比 NAS 方便）。

**第三层 · 端到端沙箱层：多沙箱看到卷内同一文件**

> ⚠️ 下面的 `<SH_PROD_API_IP>` 和 `<SH_PROD_API_KEY>` 需替换成 sh_prod 环境的实际 API 地址与团队 Key。
> Admin Token 为 `dev-admin-token-change-in-production`（见 sh_prod/api.hcl）。
> 前提：已把 api 的 `DEFAULT_PERSISTENT_VOLUME_TYPE` 改成 `vepfs` 并重启。

```bash
# 建卷（卷名带 vepfs 标识，多类型对比时一眼可辨落在哪个盘）
curl -s -X POST http://<SH_PROD_API_IP>:3000/volumes \
  -H "X-API-Key: <SH_PROD_API_KEY>" -H "Content-Type: application/json" \
  -d '{"name":"share-vepfs"}' | jq

# 起多个挂同一卷的沙箱（API 会分散调度到多台节点）
for i in $(seq 1 6); do
  curl -s -X POST http://<SH_PROD_API_IP>:3000/sandboxes \
    -H "X-API-Key: <SH_PROD_API_KEY>" -H "Content-Type: application/json" \
    -d '{"templateID":"gmx_test_ubuntu_444445","timeout":600,"volumeMounts":[{"name":"share-vepfs","path":"/mnt/data"}]}' | jq -r .sandboxID
done

# 连进一个沙箱写、再连其它沙箱读，读到同内容即共享成立
#   e2b sbx connect <id-A>  ->  echo hi > /mnt/data/probe
#   e2b sbx connect <id-B>  ->  cat /mnt/data/probe      # 期望: hi
```
> ⚠️ 沙箱 auto-pause 约 10 分钟（`e2b sbx list` 的 `End at`），起沙箱后**尽快操作**，否则 `connect` 报 `Paused sandbox not found`。
> ⚠️ **重点观察**：vePFS 的 NFS **不支持文件锁（flock）**。若 orchestrator/沙箱逻辑用到文件锁，此处可能报错——这是 vePFS 相对 NAS 的最大风险点。

**查看节点沙箱分布（Admin API）**
```bash
curl -s -X GET "http://<SH_PROD_API_IP>:3000/nodes" \
  -H "X-Admin-Token: dev-admin-token-change-in-production" \
  | jq '{nodes: [.[] | {id, sandboxCount, status}], totalSandboxCount: ([.[].sandboxCount] | add)}'
```

### 3.2 相关代码（测试代码）
```python
import requests, uuid
BASE="http://<SH_PROD_API_IP>:3000"; KEY="<SH_PROD_API_KEY>"
H={"X-API-Key":KEY,"Content-Type":"application/json"}
vn="share-vepfs-"+uuid.uuid4().hex[:6]     # 卷名带 vepfs 标识，便于区分落在哪个盘
rv=requests.post(f"{BASE}/volumes",headers=H,json={"name":vn})
assert rv.status_code in (200,201), f"建卷失败: {rv.status_code} {rv.text}"
ok=bad=0
for i in range(10):
    r=requests.post(f"{BASE}/sandboxes",headers=H,json={
        "templateID":"gmx_test_ubuntu_444445",
        "timeout":600,                       # 沙箱存活 600 秒 = 10 分钟
        "volumeMounts":[{"name":vn,"path":"/mnt/v"}],
    })
    if r.status_code==201:
        print(i, 201, r.json().get("sandboxID"))
    else:
        print(i, r.status_code, r.text)     # 失败时打印响应体
    ok += r.status_code==201; bad += r.status_code!=201
print(f"OK={ok} FAIL={bad}  volume={vn}")
```

---

## ④ 与 NAS 方案的差异（对比要点）

| 维度 | NAS（文件存储 NAS） | vePFS（本文） |
|---|---|---|
| 控制台创建 | 文件系统 + 挂载点 | 文件系统 + **NFS 协议服务** + **导出目录** |
| 协议 | NFSv3 | NFSv3（经协议服务暴露，邀测中） |
| 客户端 | `nfs-common` | `nfs-common`（相同） |
| 挂载参数 | 带 `sec=sys` | **不带 `sec=sys`** |
| 挂载地址 | 域名直连 | `导出目录ID.文件系统域名` |
| 挂载点 | `/mnt/volcano-nfs` | `/mnt/volcano-vepfs` |
| 卷类型名 | `nas` | `vepfs` |
| root 写根目录 | 可能受 root_squash 限制 | 无限制 |
| 文件锁 flock | 支持 | **不支持**（重点风险） |
| 定位 | 通用共享 | 高并发大 IO（AI 训练/推理） |

> 对比测试标准流程：orchestrator 三类型并存，每切一个盘 → 改 api `DEFAULT_PERSISTENT_VOLUME_TYPE`（`nas`/`vepfs`）→ 重启 api → 建对应卷 → 跑沙箱写读，比较行为与性能。
