# 文件传输测试
# root@dev-server-api:/mnt/nfs/test_sdk/gaomingxing/gmx_test_2026.12# python3 test_file_transfer.py
# https://49983-igxr7pxpujem2ya0b6jbn.dev-e2b.xiaobei.top
# Sandbox created: igxr7pxpujem2ya0b6jbn
# Uploaded to: /home/user/orchestrator
# ls output:
# -rw-r--r-- 1 user user 129228464 Aug 21 13:04 /home/user/orchestrator
# size output:
# 129228464 /home/user/orchestrator

from e2b_code_interpreter import Sandbox
from e2b.connection_config import ConnectionConfig
from pathlib import Path
import shlex

def patched_get_sandbox_url(self, sandbox_id, sandbox_domain):
    # 总是使用 HTTPs
    print(f"https://{self.get_host(sandbox_id, sandbox_domain, self.envd_port)}")
    return f"https://{self.get_host(sandbox_id, sandbox_domain, self.envd_port)}"

ConnectionConfig.get_sandbox_url = patched_get_sandbox_url

local_path = Path("/mnt/nfs/dev/2026.29/orchestrator")
remote_path = "/home/user/orchestrator"

sbx = Sandbox.create("test-202626-1-8")
sandbox_id = sbx.sandbox_id
print(f"Sandbox created: {sandbox_id}")

if not local_path.exists() or not local_path.is_file():
    raise RuntimeError(f"Invalid local file: {local_path}")

with local_path.open("rb") as f:
    sbx.files.write(remote_path, f)

print(f"Uploaded to: {remote_path}")

res_ls = sbx.commands.run(f"ls -l {shlex.quote(remote_path)}")
print("ls output:")
print((res_ls.stdout or res_ls.stderr or "").strip())

res_wc = sbx.commands.run(f"wc -c {shlex.quote(remote_path)}")
print("size output:")
print((res_wc.stdout or res_wc.stderr or "").strip())
