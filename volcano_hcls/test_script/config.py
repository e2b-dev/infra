"""统一测试配置"""
import os

from dotenv import load_dotenv

load_dotenv()

# === API 配置 ===
E2B_API_KEY = os.getenv("E2B_API_KEY", "e2b_4291fc7c3dabdebbd3c0d12151d4e3762a55")
E2B_BASE_URL = os.getenv("E2B_BASE_URL", "https://dev-e2b.xiaobei.top")

# === 镜像仓库配置 ===
REGISTRY_IMAGE_UBUNTU = os.getenv(
    "REGISTRY_IMAGE_UBUNTU",
    "mp-bp-cn-shanghai.cr.volces.com/e2b/ubuntu:22.04-s3",
)
REGISTRY_IMAGE_OFFICEQA = os.getenv(
    "REGISTRY_IMAGE_OFFICEQA",
    "mp-bp-cn-shanghai.cr.volces.com/north-prod-images/officeqa-v3:latest",
)
REGISTRY_USERNAME = os.getenv("REGISTRY_USERNAME", "crrobot@infrawaves")
REGISTRY_PASSWORD = os.getenv("REGISTRY_PASSWORD", "Fikypjfqobu2")

# === E2B 版本号（用于模板命名） ===
E2B_VERSION = os.getenv("E2B_VERSION", "2026.29")

# === 默认模板 / 沙箱参数 ===
DEFAULT_TEMPLATE_ID = os.getenv("TEMPLATE_ID", "base")
DEFAULT_SANDBOX_TIMEOUT = int(os.getenv("SANDBOX_TIMEOUT", "300"))
DEFAULT_CONCURRENCY = int(os.getenv("CONCURRENCY", "5"))

# === Redis (用于 vePFS 测试) ===
REDIS_HOST = os.getenv("REDIS_HOST", "192.168.162.32")

# === 文件传输测试 ===
FILE_TRANSFER_LOCAL_PATH = os.getenv(
    "FILE_TRANSFER_LOCAL_PATH",
    "/mnt/nfs/dev/2026.22/orchestrator",
)
FILE_TRANSFER_REMOTE_PATH = os.getenv(
    "FILE_TRANSFER_REMOTE_PATH",
    "/home/user/orchestrator",
)
FILE_TRANSFER_SANDBOX_TIMEOUT = int(os.getenv("FILE_TRANSFER_SANDBOX_TIMEOUT", "1800"))
FILE_TRANSFER_UPLOAD_TIMEOUT = float(os.getenv("FILE_TRANSFER_UPLOAD_TIMEOUT", "600"))

# === test1: 单模板构建 ===
TEMPLATE_BUILD_RETRIES = int(os.getenv("TEMPLATE_BUILD_RETRIES", "3"))

# === test3: 并发构建模板 ===
TEMPLATE_BUILD_CONCURRENCY = int(os.getenv("TEMPLATE_BUILD_CONCURRENCY", "10"))

# === test4: 并发创建沙箱 ===
SANDBOX_CREATE_COUNT = int(os.getenv("SANDBOX_CREATE_COUNT", str(DEFAULT_CONCURRENCY)))

# === test5: 并发创建沙箱 + 网络验证 ===
SANDBOX_NET_COUNT = int(os.getenv("SANDBOX_NET_COUNT", "3"))
SANDBOX_NET_TIMEOUT = int(os.getenv("SANDBOX_NET_TIMEOUT", "1200"))
SANDBOX_NET_CMD_TIMEOUT = int(os.getenv("SANDBOX_NET_CMD_TIMEOUT", "600"))
SANDBOX_NET_PROBE_ONLY = os.getenv("SANDBOX_NET_PROBE_ONLY", "0") == "1"

# === test7: vePFS 跨节点共享卷 ===
VEPFS_MAX_SANDBOXES = int(os.getenv("MAX_SANDBOXES", "10"))
VEPFS_CONNECT_TIMEOUT = float(os.getenv("VEPFS_CONNECT_TIMEOUT", "30"))
VEPFS_READ_TIMEOUT = float(os.getenv("VEPFS_READ_TIMEOUT", "180"))

# === 外部指定模板（跳过 test1 时使用） ===
E2B_TEMPLATE_ID = os.getenv("E2B_TEMPLATE_ID", "").strip()


def apply_env():
    """将关键变量注入 os.environ，供 SDK 使用"""
    os.environ.setdefault("E2B_API_KEY", E2B_API_KEY)
