"""统一测试用例：将原有 8 个测试脚本封装为标准接口

每个 test_xxx() 函数返回 TestResult (见 test_runner.py)。
"""
import asyncio
import json
import os
import shlex
import subprocess
import time
import traceback
import uuid
from concurrent.futures import ThreadPoolExecutor, as_completed
from pathlib import Path

import config
from config import (
    DEFAULT_CONCURRENCY,
    DEFAULT_SANDBOX_TIMEOUT,
    DEFAULT_TEMPLATE_ID,
    E2B_API_KEY,
    E2B_BASE_URL,
    E2B_TEMPLATE_ID,
    FILE_TRANSFER_LOCAL_PATH,
    FILE_TRANSFER_REMOTE_PATH,
    FILE_TRANSFER_SANDBOX_TIMEOUT,
    FILE_TRANSFER_UPLOAD_TIMEOUT,
    REDIS_HOST,
    REGISTRY_IMAGE_OFFICEQA,
    REGISTRY_IMAGE_UBUNTU,
    REGISTRY_PASSWORD,
    REGISTRY_USERNAME,
    SANDBOX_CREATE_COUNT,
    SANDBOX_NET_CMD_TIMEOUT,
    SANDBOX_NET_COUNT,
    SANDBOX_NET_PROBE_ONLY,
    SANDBOX_NET_TIMEOUT,
    TEMPLATE_BUILD_CONCURRENCY,
    TEMPLATE_BUILD_RETRIES,
    VEPFS_CONNECT_TIMEOUT,
    VEPFS_MAX_SANDBOXES,
    VEPFS_READ_TIMEOUT,
)

import requests
from e2b import AsyncTemplate, Sandbox

# test1 构建的模板 ID，供后续测试复用
_built_template_id: str | None = None


class TemplateNotBuiltError(RuntimeError):
    """test1 未成功构建模板，依赖模板的测试直接跳过"""


def _require_built_template() -> str:
    """优先使用 test1 构建出的模板；否则允许通过 E2B_TEMPLATE_ID 指定；都没有则抛异常"""
    if _built_template_id:
        return _built_template_id
    if E2B_TEMPLATE_ID:
        print(f"  ⚠️  使用环境变量 E2B_TEMPLATE_ID 指定的模板: {E2B_TEMPLATE_ID}")
        return E2B_TEMPLATE_ID
    raise TemplateNotBuiltError(
        "test1_build_template_ubuntu 未成功构建模板，且未设置 E2B_TEMPLATE_ID，跳过依赖模板的测试"
    )


# ---------------------------------------------------------------------------
# 1. 构建模板（Ubuntu 小镜像）
# ---------------------------------------------------------------------------

def test_build_template_ubuntu() -> dict:
    """从 Ubuntu 镜像构建模板，模板名含版本号+日期"""
    global _built_template_id
    config.apply_env()
    from config import E2B_VERSION
    import datetime

    date_str = datetime.datetime.now().strftime("%Y%m%d-%H%M%S")
    version_safe = E2B_VERSION.replace(".", "")
    alias = f"auto-test-ubuntu-{version_safe}-{date_str}"

    template = AsyncTemplate().from_image(
        image=REGISTRY_IMAGE_UBUNTU,
        username=REGISTRY_USERNAME,
        password=REGISTRY_PASSWORD,
    )
    logs: list[str] = []
    built_id: list[str] = []

    async def _build():
        result = await AsyncTemplate.build(
            template,
            alias=alias,
            cpu_count=1,
            memory_mb=1024,
            skip_cache=False,
            on_build_logs=lambda log: logs.append(str(log)),
        )
        if result and hasattr(result, 'template_id'):
            built_id.append(result.template_id)

    max_retries = TEMPLATE_BUILD_RETRIES
    last_err: Exception | None = None
    for attempt in range(1, max_retries + 1):
        try:
            asyncio.run(_build())
            last_err = None
            break
        except Exception as e:
            last_err = e
            print(f"  ⚠️ 构建第 {attempt}/{max_retries} 次失败: {e}")
            if attempt < max_retries:
                time.sleep(5 * attempt)
    if last_err is not None:
        raise last_err

    # 记录模板 ID：优先用 build 返回值，其次用 alias
    _built_template_id = built_id[0] if built_id else alias
    print(f"  模板已构建: {_built_template_id} (alias={alias})")

    return {
        "template_id": _built_template_id,
        "alias": alias,
        "version": E2B_VERSION,
        "logs_count": len(logs),
        "last_log": logs[-1] if logs else "",
    }


# ---------------------------------------------------------------------------
# 2. 创建单个沙箱
# ---------------------------------------------------------------------------

def test_create_single_sandbox() -> dict:
    """用 test1 构建的模板创建单个沙箱"""
    config.apply_env()
    template_id = _require_built_template()
    print(f"  使用模板: {template_id}")
    sbx = Sandbox.create(template_id, timeout=DEFAULT_SANDBOX_TIMEOUT)
    sid = sbx.sandbox_id
    return {"template_id": template_id, "sandbox_id": sid}


# ---------------------------------------------------------------------------
# 3. 并发构建模板
# ---------------------------------------------------------------------------

def test_concurrent_template_build() -> dict:
    """并发构建 N 个模板，模板名含版本号+日期时间"""
    config.apply_env()
    from config import E2B_VERSION
    import datetime

    concurrency = TEMPLATE_BUILD_CONCURRENCY
    version_safe = E2B_VERSION.replace(".", "")
    ts = datetime.datetime.now().strftime("%Y%m%d-%H%M%S")

    async def _build_one(alias: str) -> str:
        tpl = AsyncTemplate().from_image(
            image=REGISTRY_IMAGE_UBUNTU,
            username=REGISTRY_USERNAME,
            password=REGISTRY_PASSWORD,
        )
        result = await AsyncTemplate.build(
            tpl,
            alias=alias,
            cpu_count=1,
            memory_mb=1024,
            skip_cache=False,
            on_build_logs=lambda log, a=alias: None,
        )
        tid = getattr(result, "template_id", None) if result else None
        return tid or alias

    async def _main():
        aliases = [f"auto-test-tpl-{version_safe}-{ts}-{i}" for i in range(1, concurrency + 1)]
        tasks = [_build_one(a) for a in aliases]
        results = await asyncio.gather(*tasks, return_exceptions=True)
        return aliases, results

    aliases, results = asyncio.run(_main())
    succeeded_map = {a: r for a, r in zip(aliases, results) if not isinstance(r, Exception)}
    failed = {a: str(r) for a, r in zip(aliases, results) if isinstance(r, Exception)}

    print(f"  模板别名前缀: auto-test-tpl-{version_safe}-{ts}")
    for a, tid in succeeded_map.items():
        print(f"    ✅ {a}  →  template_id={tid}")
    for a, err in failed.items():
        print(f"    ❌ {a}  →  {err[:200]}")

    return {
        "total": concurrency,
        "version": E2B_VERSION,
        "aliases": aliases,
        "templates": succeeded_map,
        "succeeded": len(succeeded_map),
        "failed_count": len(failed),
        "failures": failed,
    }


# ---------------------------------------------------------------------------
# 4. 并发创建沙箱（仅创建）
# ---------------------------------------------------------------------------

def test_concurrent_sandbox_create() -> dict:
    """用 test1 构建的模板并发创建沙箱"""
    config.apply_env()
    count = SANDBOX_CREATE_COUNT
    template_id = _require_built_template()
    print(f"  使用模板: {template_id}  并发数: {count}")

    def _create_one(idx):
        try:
            sbx = Sandbox.create(template_id, timeout=DEFAULT_SANDBOX_TIMEOUT)
            try:
                host = sbx.get_host(80)
            except Exception:
                host = ""
            print(f"    ✅ #{idx}  sandbox_id={sbx.sandbox_id}  host={host}")
            return idx, sbx.sandbox_id, host, True, ""
        except Exception as e:
            err = f"{type(e).__name__}: {e}"
            print(f"    ❌ #{idx}  {err[:500]}")
            return idx, "N/A", "", False, err

    with ThreadPoolExecutor(max_workers=count) as pool:
        futures = [pool.submit(_create_one, i) for i in range(1, count + 1)]
        results = [f.result() for f in futures]

    succeeded = [r for r in results if r[3]]
    failed = [r for r in results if not r[3]]
    return {
        "total": count,
        "succeeded": len(succeeded),
        "failed_count": len(failed),
        "sandboxes": [{"index": r[0], "sandbox_id": r[1], "host": r[2]} for r in succeeded],
        "failures": [{"index": r[0], "error": r[4]} for r in failed],
    }


# ---------------------------------------------------------------------------
# 5. 并发创建沙箱 + 网络验证（apt update/install）
# ---------------------------------------------------------------------------

def test_concurrent_sandbox_network() -> dict:
    """并发创建沙箱并验证网络（apt update + apt install vim）"""
    config.apply_env()
    from https_patch import patch_https
    patch_https()

    count = SANDBOX_NET_COUNT
    template_id = _require_built_template()
    timeout = SANDBOX_NET_TIMEOUT
    cmd_timeout = SANDBOX_NET_CMD_TIMEOUT
    net_probe_only = SANDBOX_NET_PROBE_ONLY
    print(f"  使用模板: {template_id}  并发数: {count}  沙箱存活: {timeout}s  单命令超时: {cmd_timeout}s")

    def _test_one(idx):
        t0 = time.time()
        sbx = None
        try:
            sbx = Sandbox.create(template_id, timeout=timeout, allow_internet_access=True)
            try:
                host = sbx.get_host(80)
            except Exception:
                host = ""
            print(f"    ▶ #{idx}  sandbox_id={sbx.sandbox_id}  host={host}")

            sbx.commands.run("echo ready", timeout=15)

            # 网络连通性探测（快速失败）
            probe = sbx.commands.run(
                "curl -sSI -m 15 http://archive.ubuntu.com/ubuntu/ | head -1 || "
                "getent hosts archive.ubuntu.com || echo NO_DNS",
                timeout=30,
            )
            print(f"    🌐 #{idx}  网络探测: {(probe.stdout or probe.stderr).strip()[:200]}")

            if net_probe_only:
                elapsed = time.time() - t0
                return idx, sbx.sandbox_id, host, True, "probe-ok", round(elapsed, 1)

            t1 = time.time()
            proc_update = sbx.commands.run("sudo apt-get update", timeout=cmd_timeout)
            print(f"    📦 #{idx}  apt update exit={proc_update.exit_code}  耗时={time.time()-t1:.1f}s")
            if proc_update.exit_code != 0:
                raise RuntimeError(f"apt update failed (exit={proc_update.exit_code}): {(proc_update.stderr or '')[:500]}")

            t2 = time.time()
            proc_install = sbx.commands.run("sudo apt-get install -y vim", timeout=cmd_timeout)
            print(f"    📦 #{idx}  apt install exit={proc_install.exit_code}  耗时={time.time()-t2:.1f}s")
            if proc_install.exit_code != 0:
                raise RuntimeError(f"apt install failed (exit={proc_install.exit_code}): {(proc_install.stderr or '')[:500]}")

            elapsed = time.time() - t0
            return idx, sbx.sandbox_id, host, True, "ok", round(elapsed, 1)
        except Exception as e:
            elapsed = time.time() - t0
            sid = sbx.sandbox_id if sbx else "N/A"
            err = f"{type(e).__name__}: {e}"
            print(f"    ❌ #{idx}  sandbox_id={sid}  耗时={elapsed:.1f}s  {err[:600]}")
            return idx, sid, "", False, err[:800], round(elapsed, 1)

    with ThreadPoolExecutor(max_workers=count) as pool:
        futures = [pool.submit(_test_one, i) for i in range(1, count + 1)]
        results = sorted([f.result() for f in futures], key=lambda r: r[0])

    passed = [r for r in results if r[3]]
    failed = [r for r in results if not r[3]]
    times = [r[5] for r in passed]
    return {
        "total": count,
        "succeeded": len(passed),
        "failed_count": len(failed),
        "avg_time_s": round(sum(times) / len(times), 1) if times else 0,
        "sandboxes": [{"index": r[0], "sandbox_id": r[1], "host": r[2]} for r in passed],
        "failures": [{"index": r[0], "sandbox_id": r[1], "error": r[4]} for r in failed],
    }


# ---------------------------------------------------------------------------
# 6. 文件传输测试
# ---------------------------------------------------------------------------

def test_file_transfer() -> dict:
    """上传文件到沙箱并验证大小"""
    config.apply_env()
    from https_patch import patch_https
    patch_https()

    local_path = Path(FILE_TRANSFER_LOCAL_PATH)
    remote_path = FILE_TRANSFER_REMOTE_PATH

    if not local_path.exists() or not local_path.is_file():
        raise FileNotFoundError(f"本地文件不存在: {local_path}")

    local_size = local_path.stat().st_size

    template_id = _require_built_template()
    # 文件较大，需要更长的沙箱存活时间和上传请求超时
    sbx_timeout = FILE_TRANSFER_SANDBOX_TIMEOUT
    upload_timeout = FILE_TRANSFER_UPLOAD_TIMEOUT
    try:
        from e2b_code_interpreter import Sandbox as CISandbox
        sbx = CISandbox.create(template_id, timeout=sbx_timeout)
    except ImportError:
        sbx = Sandbox.create(template_id, timeout=sbx_timeout)

    try:
        host = sbx.get_host(80)
    except Exception:
        host = ""
    print(f"  sandbox_id={sbx.sandbox_id}  host={host}  文件大小={local_size} 字节")

    with local_path.open("rb") as f:
        sbx.files.write(remote_path, f, request_timeout=upload_timeout)

    res = sbx.commands.run(f"wc -c {shlex.quote(remote_path)}")
    remote_size = int((res.stdout or "").strip().split()[0])

    match = local_size == remote_size
    return {
        "sandbox_id": sbx.sandbox_id,
        "host": host,
        "local_file": str(local_path),
        "local_size": local_size,
        "remote_size": remote_size,
        "size_match": match,
    }


# ---------------------------------------------------------------------------
# 7. vePFS / NAS 跨节点共享卷测试
# ---------------------------------------------------------------------------

def test_vepfs_cross_node() -> dict:
    """创建共享卷，在不同节点的沙箱间验证数据可见性"""
    config.apply_env()
    headers = {"X-API-Key": E2B_API_KEY, "Content-Type": "application/json"}
    base_url = E2B_BASE_URL.rstrip("/")
    template_id = _require_built_template()
    mount_path = "/mnt/v"
    max_sandboxes = VEPFS_MAX_SANDBOXES
    # (connect, read) 超时；沙箱创建接口在服务端可能耗时较长
    http_timeout = (
        VEPFS_CONNECT_TIMEOUT,
        VEPFS_READ_TIMEOUT,
    )

    def _find_node(sandbox_id):
        pattern = f"sandbox:storage:*:sandboxes:{sandbox_id}"
        deadline = time.monotonic() + 15
        while time.monotonic() < deadline:
            keys = subprocess.run(
                ["redis-cli", "-h", REDIS_HOST, "--scan", "--pattern", pattern],
                capture_output=True, text=True, check=False,
            ).stdout.splitlines()
            for key in keys:
                value = subprocess.run(
                    ["redis-cli", "-h", REDIS_HOST, "--raw", "GET", key],
                    capture_output=True, text=True, check=False,
                ).stdout.strip()
                try:
                    record = json.loads(value)
                except json.JSONDecodeError:
                    continue
                for field in ("nodeID", "nodeId", "node_id"):
                    if record.get(field):
                        return str(record[field])
            time.sleep(1)
        return None

    def _create_sbx(volume_name):
        resp = requests.post(
            f"{base_url}/sandboxes", headers=headers,
            json={"templateID": template_id, "timeout": 600,
                  "volumeMounts": [{"name": volume_name, "path": mount_path}]},
            timeout=http_timeout,
        )
        if resp.status_code != 201:
            print(f"    ❌ 创建沙箱失败: status={resp.status_code} body={resp.text[:300]}")
            return None
        sid = resp.json().get("sandboxID")
        if not sid:
            return None
        nid = _find_node(sid)
        if nid:
            print(f"    ✅ sandbox_id={sid}  node={nid}")
        return (sid, nid) if nid else None

    def _exec(sandbox_id, cmd):
        r = subprocess.run(
            ["e2b", "sbx", "exec", sandbox_id, "--", "sh", "-c", cmd],
            capture_output=True, text=True, check=True,
        )
        return r.stdout.strip()

    volume_name = f"auto-test-vepfs-{uuid.uuid4().hex[:6]}"
    resp = requests.post(
        f"{base_url}/volumes", headers=headers,
        json={"name": volume_name}, timeout=http_timeout,
    )
    if resp.status_code not in (200, 201):
        raise RuntimeError(f"创建卷失败: status={resp.status_code} body={resp.text}")

    sandboxes = []
    nodes = set()
    for _ in range(max_sandboxes):
        sbx = _create_sbx(volume_name)
        if sbx is None:
            continue
        sandboxes.append(sbx)
        nodes.add(sbx[1])
        if len(nodes) >= 2:
            break

    if len(nodes) < 2:
        return {
            "volume_name": volume_name,
            "nodes": sorted(nodes),
            "sandbox_count": len(sandboxes),
            "cross_node_verified": False,
            "reason": "未能在 2 个不同节点上创建沙箱",
        }

    first_sid, first_node = sandboxes[0]
    second_sid, second_node = next(s for s in sandboxes if s[1] != first_node)
    marker = f"vepfs-{uuid.uuid4().hex}"
    marker_path = f"{mount_path}/cross-node-test.txt"
    _exec(first_sid, f"printf '%s\\n' '{marker}' > '{marker_path}'")
    observed = _exec(second_sid, f"cat '{marker_path}'")

    return {
        "volume_name": volume_name,
        "nodes": sorted(nodes),
        "write_node": first_node,
        "write_sandbox": first_sid,
        "read_node": second_node,
        "read_sandbox": second_sid,
        "expected": marker,
        "observed": observed,
        "cross_node_verified": marker == observed,
    }


# ---------------------------------------------------------------------------
# 8. 大镜像构建模板
# ---------------------------------------------------------------------------

def test_build_template_big_image() -> dict:
    """从大镜像（officeqa-v3）构建模板，模板名含版本号+日期时间"""
    config.apply_env()
    from config import E2B_VERSION
    import datetime

    date_str = datetime.datetime.now().strftime("%Y%m%d-%H%M%S")
    version_safe = E2B_VERSION.replace(".", "")
    alias = f"auto-test-officeqa-{version_safe}-{date_str}"

    template = AsyncTemplate().from_image(
        image=REGISTRY_IMAGE_OFFICEQA,
        username=REGISTRY_USERNAME,
        password=REGISTRY_PASSWORD,
    )
    logs: list[str] = []

    async def _build():
        await AsyncTemplate.build(
            template,
            alias=alias,
            cpu_count=1,
            memory_mb=1024,
            skip_cache=False,
            on_build_logs=lambda log: logs.append(str(log)),
        )

    asyncio.run(_build())
    return {"alias": alias, "version": E2B_VERSION,
            "logs_count": len(logs), "last_log": logs[-1] if logs else ""}


# ---------------------------------------------------------------------------
# 测试用例注册表
# ---------------------------------------------------------------------------

ALL_TESTS = {
    "test1_build_template_ubuntu": {
        "func": test_build_template_ubuntu,
        "name": "构建模板（Ubuntu 小镜像）",
        "category": "模板构建",
    },
    "test2_create_single_sandbox": {
        "func": test_create_single_sandbox,
        "name": "创建单个沙箱",
        "category": "沙箱创建",
    },
    "test3_concurrent_template_build": {
        "func": test_concurrent_template_build,
        "name": "并发构建模板",
        "category": "模板构建",
    },
    "test4_concurrent_sandbox_create": {
        "func": test_concurrent_sandbox_create,
        "name": "并发创建沙箱",
        "category": "沙箱创建",
    },
    "test5_concurrent_sandbox_network": {
        "func": test_concurrent_sandbox_network,
        "name": "并发创建沙箱 + 网络验证",
        "category": "沙箱网络",
    },
    "test6_file_transfer": {
        "func": test_file_transfer,
        "name": "文件传输测试",
        "category": "文件操作",
    },
    "test7_vepfs_cross_node": {
        "func": test_vepfs_cross_node,
        "name": "vePFS 跨节点共享卷",
        "category": "存储",
    },
    "test8_build_template_big_image": {
        "func": test_build_template_big_image,
        "name": "构建模板（大镜像 officeqa）",
        "category": "模板构建",
        "skip": True,
    },
}
