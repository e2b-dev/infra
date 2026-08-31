# 火山 NFS 沙箱共享持久卷 —— 操作笔记

> **目标**：让所有 orchestrator 节点上的 E2B 沙箱共享同一份火山 NAS 云盘，实现持久卷跨节点共享。
> **环境**：dev（`https://dev-e2b.xiaobei.top`）　**最后验证通过**：2026-07-29

---

## 概述：原理与整体流程

一句话原理：`PERSISTENT_VOLUME_MOUNTS = "类型名:根路径"`，卷数据落在 `{根路径}/team-{id}/vol-{id}`。
**只要每台节点把这个根路径挂到同一个火山 NAS，跨节点就共享了。**

整体三步：

```
① 火山云          ② 每台 Linux 节点              ③ 改配置 + 验证
─────────      ──────────────────      ──────────────────
创建 NAS   ──▶  挂载 NAS 到 /volcano-nfs  ──▶  改 HCL → 部署 → 验证共享
创建挂载点        配 fstab 开机自动挂载
```

---

## ① 火山云控制台操作

路径：火山引擎控制台 → **文件存储 NAS** → 文件系统列表。

### 1.1 创建文件系统
- 类型：**NAS 极速型**（通用共享场景）。
- 地域：华东2（上海）。

### 1.2 创建挂载点
| 配置项 | 取值 | 说明 |
|---|---|---|
| 私有网络 | `VPC-e2b` | **必须与 orchestrator 节点同一 VPC** |
| 子网 | `Subnet-clickhouse` | 同 VPC 内互通即可 |
| 权限组 | 默认权限组 | 注意 squash 策略（见坑表 root_squash） |
| 挂载点名称 | 任意 | 仅是标签，不影响挂载地址 |

### 1.3 记录挂载地址
挂载点状态变「运行中」后，在挂载点页复制挂载命令，记下**域名地址 + 文件系统路径**。

> 本项目实际值：
> `cnsha097461925df95.vpc-3i5xx2ldhtvk05r0lrq5fr9pd.nas.ivolces.com:/enas-cnsha097461925df95`
> ⚠️ 路径 `/enas-cnsha097461925df95` 要带上，不是 `/`。协议 NFS v3。

---

## ② Linux 服务器操作（每台节点）

> ⚠️ **每台 orchestrator 节点都要做，一台都不能漏。**
> dev 当前节点：`dev-sbx-1`、`nomad-sandbox-node-2`（API 节点 `dev-server-api` 也挂了，方便在宿主机直接查数据）。

### 2.1 安装客户端并挂载
```bash
# 1) 安装 NFS 客户端
apt-get install -y nfs-common

# 2) 建本地挂载目录
mkdir -p /volcano-nfs

# 3) 挂载（用火山控制台给的 v3 命令）
mount -t nfs -o vers=3,nolock,proto=tcp,rsize=1048576,wsize=1048576,hard,timeo=600,retrans=2,sec=sys,noresvport cnsha097461925df95.vpc-3i5xx2ldhtvk05r0lrq5fr9pd.nas.ivolces.com:/enas-cnsha097461925df95  /volcano-nfs

# 4) 建 volumes 根目录（orchestrator 启动会校验它存在；只建这一个，卷子目录自动生成）
mkdir -p /volcano-nfs/volumes

# 5) 确认是真 NFS（关键！必须 nfs，不能 ext2/3/4）
stat -f -c %T /volcano-nfs        # 期望输出: nfs
mount | grep volcano-nfs               # 期望: 火山域名 + type nfs
```

### 2.2 配置开机自动挂载
`/etc/fstab` 追加（`_netdev` 保证网络就绪后再挂，防重启后 job 起不来）：
```
cnsha097461925df95.vpc-3i5xx2ldhtvk05r0lrq5fr9pd.nas.ivolces.com:/enas-cnsha097461925df95  /volcano-nfs  nfs  vers=3,nolock,proto=tcp,rsize=1048576,wsize=1048576,hard,timeo=600,retrans=2,sec=sys,noresvport,_netdev  0  0
``` 或直接执行如下命令
echo 'cnsha097461925df95.vpc-3i5xx2ldhtvk05r0lrq5fr9pd.nas.ivolces.com:/enas-cnsha097461925df95  /volcano-nfs  nfs  vers=3,nolock,proto=tcp,rsize=1048576,wsize=1048576,hard,timeo=600,retrans=2,sec=sys,noresvport,_netdev  0  0' >> /etc/fstab

### 2.3 改 HCL 配置并部署
dev 环境 job 文件的 `PERSISTENT_VOLUME_MOUNTS` 改成 NFS 路径：
- `volcano_hcls/sh_dev/orchestrator-with-template.hcl`（原 `local:/data/e2b-sbx-share`）

```hcl
PERSISTENT_VOLUME_MOUNTS = "local:/volcano-nfs/volumes"
```

> `ORCHESTRATOR_BASE_PATH`（build/sandbox/template/kernels 等工作目录）保持本地盘不动，**只有 volumes 迁到 NFS**。
> 其它环境同理，改各自目录下的 job 文件：`volcano_hcls/{sh_prod,hk,mx_none,yd}/orchestrator-with-template.hcl`。

改完重新部署 orchestrator job，确认 running、无 `failed to access persistent volume mount` 报错。

---

## ③ 验证步骤与代码

### 3.1 验证（由浅入深，做到跨节点这步基本就够）

**第一层 · 节点层：每台都是「真 NFS 且同源」**
在**每台** orchestrator 节点执行：
```bash
stat -f -c %T /volcano-nfs     # 都必须是 nfs（有一台是 ext2/3/4 就是假共享）
mount | grep volcano-nfs            # 源都必须是同一个火山域名
```

**第二层 · 跨节点层：一台读另一台沙箱写的卷文件**
```bash
# 复用已有数据最省事；能读到即两台挂的是同一份盘
find /volcano-nfs/volumes -name '<某文件>' -exec cat {} \;
```
> 别用 `echo > /volcano-nfs/volumes/xxx` 在**根目录**直接写探针——权限组若有 root_squash，root 会写失败（`No such file / Permission denied`）。读子目录不受影响。

**第三层 · 端到端沙箱层：多沙箱看到卷内同一文件**
```bash
# 建卷（卷名带 nas 标识，多类型对比时一眼可辨落在哪个盘）
curl -s -X POST https://prod-e2b.xiaobei.top/volumes \
  -H "X-API-Key: e2b_d0cdf093c7be05bb293c7a0cff653f0bbd90" -H "Content-Type: application/json" \
  -d '{"name":"share-nas"}' | jq

# 起多个挂同一卷的沙箱（API 会分散调度到两台节点）
for i in $(seq 1 6); do
  curl -s -X POST https://prod-e2b.xiaobei.top/sandboxes \
    -H "X-API-Key: e2b_d0cdf093c7be05bb293c7a0cff653f0bbd90" -H "Content-Type: application/json" \
    -d '{"templateID":"northau-xiaobei_dd9dddefbe5773793b7d470c26f6b7c9817bbae5","timeout":600,"volumeMounts":[{"name":"share-nas","path":"/data"}]}' | jq -r .sandboxID
done

# 连进一个沙箱写、再连其它沙箱读，读到同内容即共享成立
#   e2b sbx connect <id-A>  ->  echo hi > /data/probe
#   e2b sbx connect <id-B>  ->  cat /data/probe      # 期望: hi
```
> ✅ 已验证：10 个挂同一卷的沙箱都看到卷里同一份文件，共享成立。
> ⚠️ 沙箱 auto-pause 约 10 分钟（`e2b sbx list` 的 `End at`），起沙箱后**尽快操作**，否则 `connect` 报 `Paused sandbox not found`。

**查看节点沙箱分布（Admin API）**
```bash
curl -s -X GET "https://prod-e2b.xiaobei.top/nodes" \
  -H "X-Admin-Token: dev-admin-token-change-in-production" \
  | jq '{nodes: [.[] | {id, sandboxCount, status}], totalSandboxCount: ([.[].sandboxCount] | add)}'
```

### 3.2 相关代码（测试代码）
```python
import requests, uuid
BASE="https://prod-e2b.xiaobei.top"; KEY="e2b_e17576c41c85f5010d32a87f81dfdc5ac19c"
H={"X-API-Key":KEY,"Content-Type":"application/json"}
vn="share-nas-"+uuid.uuid4().hex[:6]      # 卷名带 nas 标识，便于区分落在哪个盘
rv=requests.post(f"{BASE}/volumes",headers=H,json={"name":vn})
assert rv.status_code in (200,201), f"建卷失败: {rv.status_code} {rv.text}"
ok=bad=0
for i in range(10):
    r=requests.post(f"{BASE}/sandboxes",headers=H,json={
        "templateID":"gmx_test_ubuntu_3",
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
