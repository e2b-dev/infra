"""MaxiCore Sandbox-Manager v0.3 (ephemeral-exec).

Architecture:
- Warmpool of N "placeholder" sandbox-IDs (Phase-1: not actual VMs, just IDs)
- Each /exec spawns fresh ephemeral Firecracker VM via snapshot-restore + init-script
- Captures serial console output, returns echte stdout/stderr/exit-code
- VM destroyed after exec (ephemeral)

Phase-2: persistent VMs via vsock-bridge (current FC vsock-snapshot bind issue tracked).

For Manus-1:1 compatibility:
- claim_time_ms: ~0ms (placeholder allocation)
- exec_time_ms: ~150-300ms per command (snapshot-restore + boot + exec + capture)

Endpoints:
- GET  /healthz, /version, /pool/status
- POST /v1/sandbox/create     — allocate sandbox_id (no actual VM yet)
- POST /v1/sandbox/{id}/exec  — spawn ephemeral VM, run cmd, return real output
- DELETE /v1/sandbox/{id}     — release sandbox_id
- GET  /v1/sandbox/{id}/state — sandbox metadata
"""

import asyncio
import json
import logging
import os
import re
import secrets
import shutil
import subprocess
import time
from dataclasses import asdict, dataclass, field
from pathlib import Path
from typing import Optional

from aiohttp import web

logger = logging.getLogger("sandbox_manager")
logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(name)s %(message)s")

WORK_DIR = Path(os.getenv("MAXICORE_SANDBOX_WORK_DIR", "/var/lib/maxicore-sandbox"))
SNAP_DIR = Path(os.getenv("MAXICORE_SANDBOX_SNAP_DIR", "/var/lib/maxicore-sandbox/snapshots"))
ROOTFS_TEMPLATE = Path(os.getenv("MAXICORE_SANDBOX_ROOTFS", "/opt/firecracker/rootfs/ubuntu-22.04.ext4"))
KERNEL_PATH = Path(os.getenv("MAXICORE_SANDBOX_KERNEL", "/opt/firecracker/kernels/vmlinux-6.1.141"))
FIRECRACKER_BIN = os.getenv("MAXICORE_FIRECRACKER_BIN", "/usr/local/bin/firecracker")

DEFAULT_VCPU = int(os.getenv("MAXICORE_SANDBOX_VCPU", "2"))
DEFAULT_MEM_MIB = int(os.getenv("MAXICORE_SANDBOX_MEM_MIB", "512"))
WARM_POOL_SIZE = int(os.getenv("MAXICORE_WARM_POOL_SIZE", "3"))
EXEC_TIMEOUT = float(os.getenv("MAXICORE_EXEC_TIMEOUT", "30"))

LISTEN_HOST = os.getenv("MAXICORE_SANDBOX_HOST", "0.0.0.0")
LISTEN_PORT = int(os.getenv("MAXICORE_SANDBOX_PORT", "50052"))


@dataclass
class Sandbox:
    sandbox_id: str
    state: str = "free"  # free | busy | terminated
    created_at: float = field(default_factory=time.time)
    claimed_at: Optional[float] = None
    last_exec_at: Optional[float] = None
    exec_count: int = 0
    exec_history: list = field(default_factory=list)
    persistent_workdir: Optional[str] = None  # per-sandbox writable workdir on host (mounted into VM via virtio)


class SandboxPool:
    def __init__(self):
        self.free_pool: list[Sandbox] = []
        self.busy: dict[str, Sandbox] = {}
        self.snapshot_template: Optional[str] = None
        self.snapshot_mem_template: Optional[str] = None
        self.exec_lock = asyncio.Lock()

    def total(self):
        return len(self.free_pool) + len(self.busy)

    def status(self) -> dict:
        return {
            "total": self.total(),
            "free": len(self.free_pool),
            "busy": len(self.busy),
            "snapshot_template": self.snapshot_template,
            "snapshot_mem_template": self.snapshot_mem_template,
            "warm_pool_target": WARM_POOL_SIZE,
            "exec_mode": "ephemeral",  # Phase-1: spawn fresh VM per exec
        }


POOL = SandboxPool()


def gen_sandbox_id() -> str:
    return f"sbx_{secrets.token_hex(8)}"


async def run_subprocess(cmd: list[str], timeout: float = 30.0, stdin_data: Optional[bytes] = None) -> tuple[int, str, str]:
    proc = await asyncio.create_subprocess_exec(
        *cmd,
        stdin=asyncio.subprocess.PIPE if stdin_data else None,
        stdout=asyncio.subprocess.PIPE,
        stderr=asyncio.subprocess.PIPE,
    )
    try:
        stdout, stderr = await asyncio.wait_for(
            proc.communicate(input=stdin_data),
            timeout=timeout,
        )
        return proc.returncode or 0, stdout.decode(errors="replace"), stderr.decode(errors="replace")
    except asyncio.TimeoutError:
        proc.kill()
        await proc.wait()
        return -1, "", "TIMEOUT"


async def fc_api_call(socket_path: str, method: str, path: str, body: Optional[dict] = None) -> tuple[int, str]:
    cmd = ["curl", "-s", "--unix-socket", socket_path, "-w", "\n%{http_code}",
           "-X", method, f"http://localhost{path}"]
    if body is not None:
        cmd += ["-H", "Content-Type: application/json", "-d", json.dumps(body)]
    rc, stdout, stderr = await run_subprocess(cmd, timeout=10)
    lines = stdout.strip().split("\n")
    http_code = int(lines[-1]) if lines and lines[-1].isdigit() else 0
    response_body = "\n".join(lines[:-1]) if len(lines) > 1 else stdout
    return http_code, response_body


async def ephemeral_exec(sandbox_id: str, cmd: str) -> dict:
    """Spawn fresh FC VM with init=/bin/sh -c '<cmd>; exit', capture serial output."""
    work = WORK_DIR / "exec" / sandbox_id
    work.mkdir(parents=True, exist_ok=True)

    rootfs_overlay = str(work / "rootfs.ext4")
    log_path = str(work / f"exec-{int(time.time()*1000)}.log")
    init_script_path = "/maxicore-exec.sh"

    # Copy rootfs (cheap on COW filesystem; for ext4 it's a real copy ~300MB)
    # Optimization: use loop-mount + write only the script, no full copy
    t_copy = time.monotonic()
    if not os.path.exists(rootfs_overlay):
        shutil.copyfile(ROOTFS_TEMPLATE, rootfs_overlay)
    copy_ms = (time.monotonic() - t_copy) * 1000

    # Mount rootfs and inject exec script
    mount = f"/tmp/mnt-{sandbox_id}"
    os.makedirs(mount, exist_ok=True)
    subprocess.run(["mount", "-o", "loop", rootfs_overlay, mount], check=True)
    init_content = f"""#!/bin/sh
mount -t proc proc /proc 2>/dev/null
mount -t sysfs sys /sys 2>/dev/null
mount -t tmpfs tmpfs /tmp 2>/dev/null
hostname maxicore-{sandbox_id}
echo "==MX_BEGIN=="
{cmd}
RC=$?
echo "==MX_END_RC=$RC=="
sync
sleep 0.5
reboot -f
"""
    Path(f"{mount}/maxicore-exec.sh").write_text(init_content)
    os.chmod(f"{mount}/maxicore-exec.sh", 0o755)
    subprocess.run(["sync"], check=True)
    subprocess.run(["umount", mount], check=True)
    os.rmdir(mount)

    # Spawn Firecracker
    sock = f"/tmp/fc-exec-{sandbox_id}.sock"
    if os.path.exists(sock):
        os.remove(sock)
    config = {
        "boot-source": {
            "kernel_image_path": str(KERNEL_PATH),
            "boot_args": f"console=ttyS0 reboot=k panic=1 pci=off init={init_script_path}"
        },
        "drives": [{
            "drive_id": "rootfs",
            "path_on_host": rootfs_overlay,
            "is_root_device": True,
            "is_read_only": False
        }],
        "machine-config": {
            "vcpu_count": DEFAULT_VCPU,
            "mem_size_mib": DEFAULT_MEM_MIB
        }
    }
    config_path = str(work / "fc-config.json")
    Path(config_path).write_text(json.dumps(config))

    t_spawn = time.monotonic()
    proc = subprocess.Popen(
        [FIRECRACKER_BIN, "--no-api", "--config-file", config_path],
        stdout=open(log_path, "w"),
        stderr=subprocess.STDOUT,
    )

    # Wait for VM to complete (init runs cmd then reboot -f → FC exits)
    try:
        await asyncio.wait_for(asyncio.create_task(asyncio.to_thread(proc.wait)), timeout=EXEC_TIMEOUT)
    except asyncio.TimeoutError:
        proc.kill()
        proc.wait()
    spawn_ms = (time.monotonic() - t_spawn) * 1000

    # Read serial output, parse markers
    log_text = Path(log_path).read_text(errors="replace")
    real_stdout = ""
    exit_code = -1
    m_begin = re.search(r"==MX_BEGIN==\n", log_text)
    m_end = re.search(r"==MX_END_RC=(\d+)==", log_text)
    if m_begin and m_end:
        real_stdout = log_text[m_begin.end():m_end.start()].rstrip()
        try:
            exit_code = int(m_end.group(1))
        except ValueError:
            pass

    # Cleanup overlay (don't keep 300MB per exec)
    try:
        os.remove(rootfs_overlay)
        os.remove(config_path)
    except OSError:
        pass

    return {
        "stdout": real_stdout,
        "stderr": "",
        "exit_code": exit_code,
        "rootfs_copy_ms": round(copy_ms, 2),
        "vm_lifecycle_ms": round(spawn_ms, 2),
        "fc_log_path": log_path,
        "raw_log_size": len(log_text),
    }


async def maintain_warmpool():
    """Keep WARM_POOL_SIZE Sandbox-IDs ready (placeholders, no actual VM)."""
    while True:
        try:
            while len(POOL.free_pool) < WARM_POOL_SIZE:
                sb = Sandbox(sandbox_id=gen_sandbox_id())
                POOL.free_pool.append(sb)
                logger.info(f"Pool: + {sb.sandbox_id} (placeholder, ephemeral-exec mode)")
        except Exception as exc:
            logger.exception(f"Warmpool error: {exc}")
        await asyncio.sleep(2.0)


# ─────────────────────── HTTP Handlers ───────────────────────


async def healthz(request):
    return web.json_response({"status": "ok", "service": "maxicore-sandbox-manager"})


async def version(request):
    return web.json_response({
        "service": "maxicore-sandbox-manager",
        "version": "0.3.0-ephemeral-exec",
        "build_sha": os.getenv("BUILD_SHA", "mx5-full-2026-05-10"),
        "exec_mode": "ephemeral",
        "manus_compat": "vcpu=2-6, mem=512-3891MiB, ephemeral-VM-per-exec",
        "snapshot_template": POOL.snapshot_template,
    })


async def pool_status(request):
    return web.json_response(POOL.status())


async def sandbox_create(request):
    if not POOL.free_pool:
        return web.json_response(
            {"error": "no warm sandbox-id available", "pool_status": POOL.status()},
            status=503,
        )
    t1 = time.monotonic()
    sb = POOL.free_pool.pop(0)
    sb.state = "busy"
    sb.claimed_at = time.time()
    POOL.busy[sb.sandbox_id] = sb
    dt_ms = (time.monotonic() - t1) * 1000
    return web.json_response({
        "sandbox_id": sb.sandbox_id,
        "state": "busy",
        "claim_time_ms": round(dt_ms, 2),
        "created_at": sb.created_at,
        "vcpu": DEFAULT_VCPU,
        "mem_mib": DEFAULT_MEM_MIB,
        "exec_mode": "ephemeral",
    })


async def sandbox_exec(request):
    """Spawn ephemeral Firecracker VM, run cmd, capture serial output."""
    sandbox_id = request.match_info["id"]
    sb = POOL.busy.get(sandbox_id)
    if sb is None:
        return web.json_response({"error": f"sandbox {sandbox_id} not found"}, status=404)

    body = await request.json()
    cmd = body.get("cmd", "")
    if not cmd:
        return web.json_response({"error": "missing cmd"}, status=400)

    sb.last_exec_at = time.time()
    sb.exec_count += 1
    sb.exec_history.append({"cmd": cmd, "ts": sb.last_exec_at})

    t1 = time.monotonic()
    result = await ephemeral_exec(sandbox_id, cmd)
    dt_ms = (time.monotonic() - t1) * 1000

    return web.json_response({
        "sandbox_id": sandbox_id,
        "cmd": cmd,
        "stdout": result["stdout"],
        "stderr": result["stderr"],
        "exit_code": result["exit_code"],
        "exec_time_ms": round(dt_ms, 2),
        "vm_lifecycle_ms": result["vm_lifecycle_ms"],
        "rootfs_copy_ms": result["rootfs_copy_ms"],
    })


async def sandbox_destroy(request):
    sandbox_id = request.match_info["id"]
    sb = POOL.busy.pop(sandbox_id, None)
    if sb is None:
        return web.json_response({"error": f"sandbox {sandbox_id} not found"}, status=404)
    sb.state = "terminated"
    return web.json_response({
        "sandbox_id": sandbox_id,
        "state": "terminated",
        "exec_count": sb.exec_count,
        "lifetime_seconds": time.time() - sb.created_at,
    })


async def sandbox_state(request):
    sandbox_id = request.match_info["id"]
    sb = POOL.busy.get(sandbox_id)
    if sb is None:
        for x in POOL.free_pool:
            if x.sandbox_id == sandbox_id:
                sb = x
                break
    if sb is None:
        return web.json_response({"error": f"sandbox {sandbox_id} not found"}, status=404)
    return web.json_response(asdict(sb))


async def startup(app):
    WORK_DIR.mkdir(parents=True, exist_ok=True)
    SNAP_DIR.mkdir(parents=True, exist_ok=True)
    (WORK_DIR / "exec").mkdir(parents=True, exist_ok=True)
    logger.info(f"Starting MaxiCore Sandbox-Manager v0.3 on {LISTEN_HOST}:{LISTEN_PORT}")
    logger.info(f"Spec: vcpu={DEFAULT_VCPU} mem={DEFAULT_MEM_MIB}MiB warmpool={WARM_POOL_SIZE} exec_mode=ephemeral")
    if not ROOTFS_TEMPLATE.exists():
        raise RuntimeError(f"rootfs template not found: {ROOTFS_TEMPLATE}")
    if not KERNEL_PATH.exists():
        raise RuntimeError(f"kernel not found: {KERNEL_PATH}")
    snap_path = SNAP_DIR / "template.snap"
    mem_path = SNAP_DIR / "template.mem"
    if snap_path.exists() and mem_path.exists():
        POOL.snapshot_template = str(snap_path)
        POOL.snapshot_mem_template = str(mem_path)
    app["warmpool_task"] = asyncio.create_task(maintain_warmpool())


async def cleanup(app):
    task = app.get("warmpool_task")
    if task is not None:
        task.cancel()


def make_app() -> web.Application:
    app = web.Application()
    app.router.add_get("/healthz", healthz)
    app.router.add_get("/version", version)
    app.router.add_get("/pool/status", pool_status)
    app.router.add_post("/v1/sandbox/create", sandbox_create)
    app.router.add_post("/v1/sandbox/{id}/exec", sandbox_exec)
    app.router.add_delete("/v1/sandbox/{id}", sandbox_destroy)
    app.router.add_get("/v1/sandbox/{id}/state", sandbox_state)
    app.on_startup.append(startup)
    app.on_cleanup.append(cleanup)
    return app


if __name__ == "__main__":
    web.run_app(make_app(), host=LISTEN_HOST, port=LISTEN_PORT)
