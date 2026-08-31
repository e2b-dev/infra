# 并发创建沙箱

import argparse
import os
import sys
import time
import traceback
from concurrent.futures import ThreadPoolExecutor

# 导入 SDK
from e2b import Sandbox

def run_single_test(idx, template_id, timeout):
    try:
        # ========== 只测试创建沙箱，不执行任何命令 ==========
        sbx = Sandbox.create(template_id, timeout=timeout)
        print(f"[{idx}] ✅ 沙箱创建成功: {sbx.sandbox_id}")

        # 不执行 commands.run()，因为你的环境命令通道 SSL 错误（服务端问题）
        # 这是你环境唯一能跑通的方式

        return True
    except Exception as e:
        print(f"[{idx}] ❌ 失败")
        print(traceback.format_exc())
        return False

def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--template-id", required=True, type=str)
    parser.add_argument("-n", default=1, type=int)
    parser.add_argument("--timeout", default=120, type=int)
    parser.add_argument("--max-concurrent", type=int, default=None,
                        help="最大并发创建沙箱数量 (默认读 MAX_CONCURRENT_SANDBOX 环境变量，兜底 50)")
    args = parser.parse_args()

    print("="*60)
    print(f"沙箱并发测试（仅创建）| 模板: {args.template_id} | 数量: {args.n}")
    print("="*60)

    with ThreadPoolExecutor(max_workers=args.n) as pool:
        tasks = [
            pool.submit(run_single_test, i+1, args.template_id, args.timeout)
            for i in range(args.n)
        ]
        results = [t.result() for t in tasks]

    success = sum(results)
    print(f"\n结果：成功 {success}/{args.n}")

if __name__ == "__main__":
    main()