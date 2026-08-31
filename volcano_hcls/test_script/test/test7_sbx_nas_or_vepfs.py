# 13. vePFS 或 NAS 测试
# 跑这个脚本时注意看结果必须在不同节点创建沙箱，不然可能是错误结论！

import json
import os
import subprocess
import time
import uuid

import requests

BASE_URL = os.getenv("E2B_BASE_URL", "https://dev-e2b.xiaobei.top").rstrip("/")
API_KEY = os.getenv("E2B_API_KEY", "e2b_4291fc7c3dabdebbd3c0d12151d4e3762a55")
REDIS_HOST = os.getenv("REDIS_HOST", "192.168.162.32")
TEMPLATE_ID = os.getenv("E2B_TEMPLATE_ID", "gmx_test_ubuntu_202629")
MAX_SANDBOXES = int(os.getenv("MAX_SANDBOXES", "10"))
NODE_LOOKUP_TIMEOUT = int(os.getenv("NODE_LOOKUP_TIMEOUT", "15"))
MOUNT_PATH = "/mnt/v"
HEADERS = {"X-API-Key": API_KEY, "Content-Type": "application/json"}

def find_node_id(sandbox_id: str) -> str | None:
    pattern = f"sandbox:storage:*:sandboxes:{sandbox_id}"
    deadline = time.monotonic() + NODE_LOOKUP_TIMEOUT
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

def create_sandbox(volume_name: str) -> tuple[str, str] | None:
    response = requests.post(
        f"{BASE_URL}/sandboxes",
        headers=HEADERS,
        json={
            "templateID": TEMPLATE_ID,
            "timeout": 600,
            "volumeMounts": [{"name": volume_name, "path": MOUNT_PATH}],
        },
        timeout=30,
    )
    if response.status_code != 201:
        print(f"CREATE FAILED status={response.status_code} body={response.text}")
        return None
    sandbox_id = response.json().get("sandboxID")
    if not sandbox_id:
        print(f"CREATE FAILED missing sandboxID body={response.text}")
        return None
    node_id = find_node_id(sandbox_id)
    print(f"SANDBOX sandboxID={sandbox_id} nodeID={node_id or 'UNKNOWN'}")
    return (sandbox_id, node_id) if node_id else None

def run_in_sandbox(sandbox_id: str, command: str) -> str:
    result = subprocess.run(
        ["e2b", "sbx", "exec", sandbox_id, "--", "sh", "-c", command],
        capture_output=True, text=True, check=False,
    )
    if result.returncode != 0:
        raise RuntimeError(
            f"sandbox command failed sandboxID={sandbox_id} "
            f"exit={result.returncode} stderr={result.stderr.strip()}"
        )
    return result.stdout.strip()

def main() -> int:
    volume_name = f"share-vepfs-{uuid.uuid4().hex[:6]}"
    response = requests.post(
        f"{BASE_URL}/volumes", headers=HEADERS,
        json={"name": volume_name}, timeout=30,
    )
    if response.status_code not in (200, 201):
        print(f"VOLUME CREATE FAILED status={response.status_code} body={response.text}")
        return 1

    print(f"VOLUME CREATED name={volume_name} volumeID={response.json().get('volumeID')}")
    print("TEST REQUIREMENT: at least 2 different nodeIDs are required")

    sandboxes: list[tuple[str, str]] = []
    nodes: set[str] = set()
    for _ in range(MAX_SANDBOXES):
        sandbox = create_sandbox(volume_name)
        if sandbox is None:
            continue
        sandboxes.append(sandbox)
        nodes.add(sandbox[1])
        if len(nodes) >= 2:
            break

    print(f"NODE SUMMARY nodes={sorted(nodes)} sandbox_count={len(sandboxes)}")
    if len(nodes) < 2:
        print("INCONCLUSIVE: sandboxes were not placed on 2 different nodes")
        print("No cross-node shared-volume result is reported.")
        return 2

    first_sandbox, first_node = sandboxes[0]
    second_sandbox, second_node = next(item for item in sandboxes if item[1] != first_node)
    marker = f"vepfs-cross-node-{uuid.uuid4().hex}"
    marker_path = f"{MOUNT_PATH}/cross-node-test.txt"
    run_in_sandbox(first_sandbox, f"printf '%s\\n' '{marker}' > '{marker_path}'")
    observed = run_in_sandbox(second_sandbox, f"cat '{marker_path}'")

    print(f"WRITE sandboxID={first_sandbox} nodeID={first_node} value={marker}")
    print(f"READ  sandboxID={second_sandbox} nodeID={second_node} value={observed}")
    if observed != marker:
        print("")
        print("=" * 60)
        print("  ❌  FAIL: VEPFS 跨节点共享验证失败")
        print("=" * 60)
        print(f"  写入节点: {first_node}")
        print(f"  读取节点: {second_node}")
        print(f"  期望值:   {marker}")
        print(f"  实际值:   {observed}")
        print("=" * 60)
        return 1

    print("")
    print("=" * 60)
    print("  ✅  PASS: VEPFS 跨节点共享验证成功!")
    print("=" * 60)
    print(f"  卷名称:   {volume_name}")
    print(f"  写入节点: {first_node} (sandbox={first_sandbox})")
    print(f"  读取节点: {second_node} (sandbox={second_sandbox})")
    print(f"  验证数据: {marker}")
    print(f"  结论:     节点 {first_node} 写入的数据，节点 {second_node} 可以读到，VEPFS 共享卷正常工作")
    print("=" * 60)
    return 0

if __name__ == "__main__":
    raise SystemExit(main())
