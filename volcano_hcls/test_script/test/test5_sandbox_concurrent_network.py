# 并发创建沙箱并验证网络
# python3 test_sandbox_concurrent_network.py -n 20 --max-concurrent 20
# 命令详细解释：
# - -n 10  要创建的沙箱总数为10个
# - --timeout 60 沙箱操作的超时时间为60秒
# - --max-concurrent 20 最大并发创建沙箱的数量为20
# - test_sandbox_concurrent_network.py

"""并发创建沙箱测试"""
import argparse
import os
import sys
import time
import random
from concurrent.futures import ThreadPoolExecutor, as_completed
from threading import Lock
from e2b.connection_config import ConnectionConfig

def patched_get_sandbox_url(self, sandbox_id, sandbox_domain):
    # 总是使用 HTTPs
    print(f"https://{self.get_host(sandbox_id, sandbox_domain, self.envd_port)}")
    return f"https://{self.get_host(sandbox_id, sandbox_domain, self.envd_port)}"

ConnectionConfig.get_sandbox_url = patched_get_sandbox_url

try:
    from dotenv import load_dotenv
    load_dotenv()
except ImportError:
    pass

from e2b import Sandbox

def _is_retryable_error(exc):
    s = str(exc).lower()
    return (
        "500" in s
        or "504" in s
        or "failed to place sandbox" in s
        or "no nodes available" in s
        or "ssl" in s
        or "unexpected_eof" in s
        or "eof occurred" in s
        or "connection timed out" in s
        or "errno 110" in s
        or "connection reset" in s
        or "errno 104" in s
        or "broken pipe" in s
        or "context deadline exceeded" in s  # 添加超时错误为可重试
    )

# 全局锁，用于控制并发执行 apt 操作
apt_lock = Lock()
# 记录正在执行 apt 操作的沙箱数量
apt_in_progress = 0
# 最大并发执行 apt 操作的沙箱数量
MAX_CONCURRENT_APT = 10

def cleanup_apt(sandbox):
    """清理 apt 相关的进程和锁定文件"""
    try:
        print(f"[DEBUG] Cleaning up apt processes in {sandbox.sandbox_id}...")
        sandbox.commands.run("sudo pkill -9 apt", timeout=10)
        sandbox.commands.run("sudo rm -f /var/lib/apt/lists/lock /var/lib/dpkg/lock /var/lib/dpkg/lock-frontend", timeout=10)
        print(f"[DEBUG] Apt cleanup completed in {sandbox.sandbox_id}")
    except Exception as e:
        print(f"[ERROR] Failed to cleanup apt in {sandbox.sandbox_id}: {e}")

def create_sandbox_with_retry(template_id, timeout=600, retries=None):
    # 在函数开始时声明全局变量
    global apt_in_progress

    if retries is None:
        retries = int(os.getenv("SANDBOX_CREATE_RETRIES", "3"))

    for attempt in range(1, retries + 1):
        sandbox = None
        try:
            # 1. 创建沙盒
            try:
                sandbox = Sandbox.create(template_id, timeout=timeout, allow_internet_access=True)
            except TypeError:
                sandbox = Sandbox.create(template_id, timeout=timeout)

            print(f"[DEBUG] Sandbox {sandbox.sandbox_id} created. Waiting for it to be ready...")

            # 2. 等待沙盒就绪
            max_ready_checks = 10
            ready_check_interval = 1  # 秒

            for check_attempt in range(max_ready_checks):
                try:
                    sandbox.commands.run("echo hello", timeout=5)
                    print(f"[DEBUG] Sandbox {sandbox.sandbox_id} is ready after {check_attempt + 1} check(s).")
                    break
                except Exception as ready_err:
                    if check_attempt == max_ready_checks - 1:
                        raise RuntimeError(f"Sandbox {sandbox.sandbox_id} did not become ready after {max_ready_checks}s. Last error: {ready_err}")
                    print(f"[DEBUG] Sandbox not ready yet (attempt {check_attempt + 1}/{max_ready_checks}), waiting... {ready_err}")
                    time.sleep(ready_check_interval)

            # 3. 执行 apt update - 添加并发控制和重试逻辑
            try:
                print(f"[DEBUG] Starting apt update in {sandbox.sandbox_id}...")

                # 使用全局锁控制并发 apt 操作
                with apt_lock:
                    apt_in_progress += 1
                    print(f"[DEBUG] Current apt operations in progress: {apt_in_progress}")

                try:
                    # 增加超时时间到600秒
                    proc = sandbox.commands.run("sudo apt update", timeout=600)

                    if proc.exit_code != 0:
                        error_msg = proc.stderr[-500:] if len(proc.stderr) > 500 else proc.stderr
                        raise RuntimeError(f"{sandbox.sandbox_id} apt update failed (code {proc.exit_code}): {error_msg}")

                    print(f"[DEBUG] apt update successful in {sandbox.sandbox_id}.")
                finally:
                    with apt_lock:
                        apt_in_progress -= 1
                        print(f"[DEBUG] Current apt operations in progress: {apt_in_progress}")
            except Exception as exc:
                # 如果是超时错误，尝试重试
                if "context deadline exceeded" in str(exc).lower() and attempt < retries:
                    print(f"[DEBUG] apt update timed out, retrying...")
                    # 清理可能卡住的apt进程
                    cleanup_apt(sandbox)
                    # 继续下一次尝试
                    continue
                raise RuntimeError(f"{sandbox.sandbox_id} Failed to execute apt update: {str(exc)}")

            # 4. 执行 apt install vim - 添加并发控制和重试逻辑
            try:
                print(f"[DEBUG] Starting apt install vim in {sandbox.sandbox_id}...")

                # 使用全局锁控制并发 apt 操作
                with apt_lock:
                    apt_in_progress += 1
                    print(f"[DEBUG] Current apt operations in progress: {apt_in_progress}")

                try:
                    # 增加超时时间到600秒
                    proc = sandbox.commands.run("sudo apt install -y vim", timeout=600)

                    if proc.exit_code != 0:
                        error_msg = proc.stderr[-500:] if len(proc.stderr) > 500 else proc.stderr
                        raise RuntimeError(f"{sandbox.sandbox_id} apt install vim failed (code {proc.exit_code}): {error_msg}")

                    print(f"[DEBUG] apt install vim successful in {sandbox.sandbox_id}.")
                finally:
                    with apt_lock:
                        apt_in_progress -= 1
                        print(f"[DEBUG] Current apt operations in progress: {apt_in_progress}")
            except Exception as exc:
                # 如果是超时错误，尝试重试
                if "context deadline exceeded" in str(exc).lower() and attempt < retries:
                    print(f"[DEBUG] apt install vim timed out, retrying...")
                    # 清理可能卡住的apt进程
                    cleanup_apt(sandbox)
                    # 继续下一次尝试
                    continue
                raise RuntimeError(f"{sandbox.sandbox_id} Failed to execute apt install vim: {str(exc)}")

            return sandbox

        except Exception as exc:
            # 检查是否是可重试的错误
            is_retryable = _is_retryable_error(exc) or "route request" in str(exc).lower()

            if attempt < retries and is_retryable:
                delay = min(2 ** attempt, 15) + random.uniform(0, 2)  # 添加随机延迟，避免多个重试同时发生
                print(f"Sandbox operation failed (attempt {attempt}/{retries}), retry in {delay:.2f}s. Error: {exc}", file=sys.stderr)
                time.sleep(delay)
            else:
                # 清理失败的沙箱
                if sandbox:
                    try:
                        print(f"[DEBUG] Cleaning up failed sandbox {sandbox.sandbox_id}...")
                        sandbox.kill()
                        print(f"[DEBUG] Failed sandbox {sandbox.sandbox_id} cleaned up")
                    except Exception as e:
                        print(f"[ERROR] Failed to cleanup sandbox {sandbox.sandbox_id}: {e}")
                raise
    raise RuntimeError("create_sandbox_with_retry: unexpected")

def test_one_sandbox(index, template_id, timeout):
    """创建一个沙箱，返回 (index, sandbox_id, success, detail, elapsed)"""
    t0 = time.time()
    sbx = None
    try:
        sbx = create_sandbox_with_retry(template_id, timeout=timeout)
        sid = sbx.sandbox_id
        elapsed = time.time() - t0
        print(f"[#{index}] 沙箱已创建: {sid}  耗时 {elapsed:.1f}s")
        return index, sid, True, "ok", elapsed

    except Exception as exc:
        elapsed = time.time() - t0
        sid = sbx.sandbox_id if sbx else "N/A"
        print(f"[#{index}] {sid} 异常: {exc}", file=sys.stderr)
        return index, sid, False, str(exc)[:200], elapsed

def main():
    parser = argparse.ArgumentParser(description="并发创建沙箱测试")
    parser.add_argument("-n", "--count", type=int, default=None,
                        help="并发沙箱数量 (默认读 CONCURRENT_COUNT 环境变量，兜底 5)")
    parser.add_argument("--timeout", type=int, default=600,
                        help="沙箱超时时间(秒)")
    parser.add_argument("--max-concurrent", type=int, default=None,
                        help="最大并发创建沙箱数量 (默认读 MAX_CONCURRENT_SANDBOX 环境变量，兜底 50)")
    args = parser.parse_args()

    count = args.count or int(os.getenv("CONCURRENT_COUNT", "5"))
    max_concurrent = args.max_concurrent or int(os.getenv("MAX_CONCURRENT_SANDBOX", "50"))
    template_id = os.getenv("TEMPLATE_ID", "base")

    print("=" * 60)
    print(f"并发创建 {count} 个沙箱测试")
    print(f"模板: {template_id}   超时: {args.timeout}s   最大并发: {max_concurrent}")
    print("=" * 60)

    total_start = time.time()
    results = []

    # 限制并发创建沙箱的数量
    with ThreadPoolExecutor(max_workers=max_concurrent) as pool:
        futures = {
            pool.submit(test_one_sandbox, i, template_id, args.timeout): i
            for i in range(1, count + 1)
        }
        for fut in as_completed(futures):
            results.append(fut.result())

    results.sort(key=lambda r: r[0])
    total_elapsed = time.time() - total_start

    # 汇总
    passed = [r for r in results if r[2]]
    failed = [r for r in results if not r[2]]

    print("\n" + "=" * 60)
    print("测试结果汇总")
    print("=" * 60)
    print(f"总数: {count}   成功: {len(passed)}   失败: {len(failed)}   总耗时: {total_elapsed:.1f}s")

    if passed:
        times = [r[4] for r in passed]
        print(f"成功沙箱耗时 — 最小: {min(times):.1f}s  最大: {max(times):.1f}s  平均: {sum(times)/len(times):.1f}s")

    if failed:
        print("\n失败详情:")
        for idx, sid, _, detail, elapsed in failed:
            print(f"  [#{idx}] {sid} ({elapsed:.1f}s): {detail}")

    print()
    for idx, sid, ok, detail, elapsed in results:
        status = "PASS" if ok else "FAIL"
        print(f"  [#{idx}] {status}  {sid}  {elapsed:.1f}s")

    if failed:
        print(f"\n测试失败: {len(failed)}/{count} 个沙箱创建失败")
        sys.exit(1)
    else:
        print(f"\n测试通过: 全部 {count} 个沙箱创建成功")

if __name__ == "__main__":
    main()
