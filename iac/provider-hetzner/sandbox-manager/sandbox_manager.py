"""MaxiCore Sandbox-Manager v0.4 — PERSISTENT VMs via TAP+SSH (Manus-1:1 multi-step state).

Architecture change vs v0.3:
- v0.3: ephemeral-per-cmd (each /exec spawns fresh FC, state lost)
- v0.4: persistent VMs cold-boot once on /create, SSH-exec for each /exec, state preserved

Cold-boot ~1.5s per claim. Multi-step file_write→file_edit→file_read works.

Pool of 10 TAP-devices on PRIMARY: mxtap0..mxtap9, host=172.16.<i*4>.1/30, vm=172.16.<i*4>.2/30.

Endpoints:
- GET  /healthz, /version, /pool/status
- POST /v1/sandbox/create     — cold-boot fresh persistent VM, return sandbox_id+IP
- POST /v1/sandbox/{id}/exec  — SSH command in VM, state preserved across calls
- DELETE /v1/sandbox/{id}     — kill FC + free TAP slot
- GET  /v1/sandbox/{id}/state — sandbox metadata
"""

import asyncio
import json
import logging
import os
import secrets
import subprocess
import time
from dataclasses import asdict, dataclass, field
from pathlib import Path
from typing import Optional

from aiohttp import web

logger = logging.getLogger("sandbox_manager")
logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(name)s %(message)s")

WORK_DIR = Path(os.getenv("MAXICORE_SANDBOX_WORK_DIR", "/var/lib/maxicore-sandbox"))
ROOTFS_TEMPLATE = Path(os.getenv("MAXICORE_SANDBOX_ROOTFS", "/opt/firecracker/rootfs/ubuntu-22.04-v3-persist.ext4"))
KERNEL_PATH = Path(os.getenv("MAXICORE_SANDBOX_KERNEL", "/opt/firecracker/kernels/vmlinux-6.1.141"))
FIRECRACKER_BIN = os.getenv("MAXICORE_FIRECRACKER_BIN", "/usr/local/bin/firecracker")
SSH_KEY = Path(os.getenv("MAXICORE_SANDBOX_SSH_KEY", "/etc/maxicore-sandbox/ssh/sandbox_id_ed25519"))

DEFAULT_VCPU = int(os.getenv("MAXICORE_SANDBOX_VCPU", "2"))
DEFAULT_MEM_MIB = int(os.getenv("MAXICORE_SANDBOX_MEM_MIB", "1024"))
NUM_TAP_SLOTS = int(os.getenv("MAXICORE_TAP_POOL_SIZE", "10"))

LISTEN_HOST = os.getenv("MAXICORE_SANDBOX_HOST", "0.0.0.0")
LISTEN_PORT = int(os.getenv("MAXICORE_SANDBOX_PORT", "50052"))

EXEC_TIMEOUT = float(os.getenv("MAXICORE_EXEC_TIMEOUT", "30"))
SSH_BOOT_WAIT = float(os.getenv("MAXICORE_SSH_BOOT_WAIT", "5"))


@dataclass
class Sandbox:
    sandbox_id: str
    state: str = "booting"
    tap_slot: int = -1
    tap_name: str = ""
    host_ip: str = ""
    vm_ip: str = ""
    fc_pid: Optional[int] = None
    api_socket: Optional[str] = None
    rootfs_overlay: Optional[str] = None
    fc_log_path: Optional[str] = None
    created_at: float = field(default_factory=time.time)
    claimed_at: Optional[float] = None
    last_exec_at: Optional[float] = None
    exec_count: int = 0
    boot_time_ms: Optional[float] = None


class SandboxPool:
    def __init__(self):
        # tap_slot indices 0..NUM_TAP_SLOTS-1 → either None (free) or sandbox_id (claimed)
        self.slot_owners: dict[int, Optional[str]] = {i: None for i in range(NUM_TAP_SLOTS)}
        self.busy: dict[str, Sandbox] = {}

    def free_slot(self) -> Optional[int]:
        for i, owner in self.slot_owners.items():
            if owner is None:
                return i
        return None

    def status(self) -> dict:
        free = sum(1 for o in self.slot_owners.values() if o is None)
        return {
            "total_slots": NUM_TAP_SLOTS,
            "free_slots": free,
            "busy_slots": NUM_TAP_SLOTS - free,
            "exec_mode": "persistent-tap-ssh",
            "vcpu": DEFAULT_VCPU,
            "mem_mib": DEFAULT_MEM_MIB,
            "rootfs": str(ROOTFS_TEMPLATE),
        }


POOL = SandboxPool()


def gen_sandbox_id() -> str:
    return f"sbx_{secrets.token_hex(8)}"


def slot_to_ips(slot: int) -> tuple[str, str, str]:
    """Returns (tap_name, host_ip, vm_ip) for slot index."""
    base = slot * 4
    return f"mxtap{slot}", f"172.16.{base}.1", f"172.16.{base}.2"


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


async def cold_boot_persistent_vm(sandbox_id: str, slot: int) -> Sandbox:
    """Cold-boot fresh FC VM with TAP+SSH config. ~1.5s."""
    tap_name, host_ip, vm_ip = slot_to_ips(slot)
    work = WORK_DIR / "sessions" / sandbox_id
    work.mkdir(parents=True, exist_ok=True)

    # Per-VM rootfs overlay (snapshot from template, COW would be better but ext4)
    rootfs_overlay = str(work / "rootfs.ext4")
    subprocess.run(["cp", str(ROOTFS_TEMPLATE), rootfs_overlay], check=True)

    sock = f"/tmp/fc-{sandbox_id}.sock"
    fc_log = str(work / "fc.log")
    if os.path.exists(sock):
        os.remove(sock)

    # Generate unique MAC per slot to avoid collision: 02:fc:00:00:00:<slot>
    mac = f"02:fc:00:00:00:{slot:02x}"

    # Kernel cmdline with ip= for static IP setup at boot
    # ip=client-ip::gateway-ip:netmask::eth0:off
    boot_args = (
        "console=ttyS0 reboot=k panic=1 pci=off "
        f"init=/maxicore-init.sh "
        f"ip={vm_ip}::{host_ip}:255.255.255.252::eth0:off"
    )

    # Spawn FC with API
    proc = subprocess.Popen(
        [FIRECRACKER_BIN, "--api-sock", sock],
        stdout=open(fc_log, "w"),
        stderr=subprocess.STDOUT,
    )
    await asyncio.sleep(0.2)

    # Configure VM
    code, body = await fc_api_call(sock, "PUT", "/machine-config",
                                    {"vcpu_count": DEFAULT_VCPU, "mem_size_mib": DEFAULT_MEM_MIB})
    if code != 204:
        proc.kill()
        raise RuntimeError(f"machine-config failed {code}: {body}")

    code, body = await fc_api_call(sock, "PUT", "/boot-source",
                                    {"kernel_image_path": str(KERNEL_PATH), "boot_args": boot_args})
    if code != 204:
        proc.kill()
        raise RuntimeError(f"boot-source failed {code}: {body}")

    code, body = await fc_api_call(sock, "PUT", "/drives/rootfs",
                                    {"drive_id": "rootfs", "path_on_host": rootfs_overlay,
                                     "is_root_device": True, "is_read_only": False})
    if code != 204:
        proc.kill()
        raise RuntimeError(f"drives failed {code}: {body}")

    # Network interface — UNIQUE TAP per VM
    code, body = await fc_api_call(sock, "PUT", "/network-interfaces/eth0",
                                    {"iface_id": "eth0", "host_dev_name": tap_name,
                                     "guest_mac": mac})
    if code != 204:
        proc.kill()
        raise RuntimeError(f"network-interfaces failed {code}: {body}")

    # Bring up TAP first (in case it was DOWN)
    subprocess.run(["ip", "link", "set", tap_name, "up"], check=False)

    # Start VM
    t_start = time.monotonic()
    code, body = await fc_api_call(sock, "PUT", "/actions",
                                    {"action_type": "InstanceStart"})
    if code != 204:
        proc.kill()
        raise RuntimeError(f"InstanceStart failed {code}: {body}")

    # Wait for SSH to be reachable
    ssh_ready = False
    boot_deadline = time.monotonic() + SSH_BOOT_WAIT
    while time.monotonic() < boot_deadline:
        rc, _, _ = await run_subprocess(
            ["ssh", "-i", str(SSH_KEY), "-o", "StrictHostKeyChecking=no",
             "-o", "UserKnownHostsFile=/dev/null", "-o", "ConnectTimeout=2",
             "-o", "BatchMode=yes", f"root@{vm_ip}", "echo READY"],
            timeout=3,
        )
        if rc == 0:
            ssh_ready = True
            break
        await asyncio.sleep(0.2)

    boot_ms = (time.monotonic() - t_start) * 1000
    if not ssh_ready:
        proc.kill()
        raise RuntimeError(f"SSH not reachable after {SSH_BOOT_WAIT}s on {vm_ip}")

    sb = Sandbox(
        sandbox_id=sandbox_id,
        state="ready",
        tap_slot=slot,
        tap_name=tap_name,
        host_ip=host_ip,
        vm_ip=vm_ip,
        fc_pid=proc.pid,
        api_socket=sock,
        rootfs_overlay=rootfs_overlay,
        fc_log_path=fc_log,
        boot_time_ms=round(boot_ms, 2),
    )
    return sb


# ─────────────────────── HTTP Handlers ───────────────────────


async def healthz(request):
    return web.json_response({"status": "ok", "service": "maxicore-sandbox-manager"})


async def version(request):
    return web.json_response({
        "service": "maxicore-sandbox-manager",
        "version": "0.4.0-persistent-tap-ssh",
        "build_sha": os.getenv("BUILD_SHA", "mx10-persistent-vms"),
        "exec_mode": "persistent-cold-boot-tap-ssh",
        "manus_compat": "vcpu=2-6, mem=1024-3891MiB, persistent VMs with multi-step state",
    })


async def pool_status(request):
    return web.json_response(POOL.status())


async def sandbox_create(request):
    """Cold-boot fresh persistent VM, return sandbox_id."""
    slot = POOL.free_slot()
    if slot is None:
        return web.json_response(
            {"error": "no free TAP slot", "pool_status": POOL.status()},
            status=503,
        )

    sandbox_id = gen_sandbox_id()
    POOL.slot_owners[slot] = sandbox_id  # claim

    try:
        sb = await cold_boot_persistent_vm(sandbox_id, slot)
        sb.claimed_at = time.time()
        POOL.busy[sandbox_id] = sb
        return web.json_response({
            "sandbox_id": sb.sandbox_id,
            "state": sb.state,
            "tap_slot": sb.tap_slot,
            "tap_name": sb.tap_name,
            "host_ip": sb.host_ip,
            "vm_ip": sb.vm_ip,
            "boot_time_ms": sb.boot_time_ms,
            "fc_pid": sb.fc_pid,
            "vcpu": DEFAULT_VCPU,
            "mem_mib": DEFAULT_MEM_MIB,
            "exec_mode": "persistent-ssh",
        })
    except Exception as exc:
        POOL.slot_owners[slot] = None
        logger.exception(f"create sandbox failed: {exc}")
        return web.json_response({"error": f"VM boot failed: {exc}"}, status=500)


async def sandbox_exec(request):
    """SSH command in persistent VM. State preserved across calls."""
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

    t1 = time.monotonic()
    rc, stdout, stderr = await run_subprocess(
        ["ssh", "-i", str(SSH_KEY), "-o", "StrictHostKeyChecking=no",
         "-o", "UserKnownHostsFile=/dev/null", "-o", "ConnectTimeout=5",
         "-o", "BatchMode=yes", f"root@{sb.vm_ip}", cmd],
        timeout=EXEC_TIMEOUT,
    )
    dt_ms = (time.monotonic() - t1) * 1000

    return web.json_response({
        "sandbox_id": sandbox_id,
        "cmd": cmd,
        "stdout": stdout,
        "stderr": stderr,
        "exit_code": rc,
        "exec_time_ms": round(dt_ms, 2),
        "vm_ip": sb.vm_ip,
        "exec_count": sb.exec_count,
    })


async def sandbox_destroy(request):
    sandbox_id = request.match_info["id"]
    sb = POOL.busy.pop(sandbox_id, None)
    if sb is None:
        return web.json_response({"error": f"sandbox {sandbox_id} not found"}, status=404)

    # Kill FC
    if sb.fc_pid:
        try:
            os.kill(sb.fc_pid, 15)
            await asyncio.sleep(0.3)
            os.kill(sb.fc_pid, 9)
        except (OSError, ProcessLookupError):
            pass

    # Cleanup files
    for path in [sb.api_socket, sb.rootfs_overlay]:
        if path and os.path.exists(path):
            try:
                os.remove(path)
            except OSError:
                pass

    # Free slot
    POOL.slot_owners[sb.tap_slot] = None
    sb.state = "terminated"

    return web.json_response({
        "sandbox_id": sandbox_id,
        "state": "terminated",
        "exec_count": sb.exec_count,
        "lifetime_seconds": time.time() - sb.created_at,
        "tap_slot_freed": sb.tap_slot,
    })


async def sandbox_state(request):
    sandbox_id = request.match_info["id"]
    sb = POOL.busy.get(sandbox_id)
    if sb is None:
        return web.json_response({"error": f"sandbox {sandbox_id} not found"}, status=404)
    return web.json_response(asdict(sb))


async def startup(app):
    WORK_DIR.mkdir(parents=True, exist_ok=True)
    (WORK_DIR / "sessions").mkdir(parents=True, exist_ok=True)
    logger.info(f"Starting MaxiCore Sandbox-Manager v0.4 PERSISTENT on {LISTEN_HOST}:{LISTEN_PORT}")
    logger.info(f"TAP-Pool: {NUM_TAP_SLOTS} slots, vcpu={DEFAULT_VCPU} mem={DEFAULT_MEM_MIB}MiB")
    if not ROOTFS_TEMPLATE.exists():
        raise RuntimeError(f"rootfs template not found: {ROOTFS_TEMPLATE}")
    if not KERNEL_PATH.exists():
        raise RuntimeError(f"kernel not found: {KERNEL_PATH}")
    if not SSH_KEY.exists():
        raise RuntimeError(f"SSH key not found: {SSH_KEY}")


async def cleanup(app):
    for sb in list(POOL.busy.values()):
        try:
            os.kill(sb.fc_pid, 9)
        except (OSError, ProcessLookupError):
            pass


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
