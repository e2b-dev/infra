#!/usr/bin/env python3
"""
E2B 平台统一自动化测试运行器

功能：
  - 运行全部或指定测试用例
  - 输出结构化 JSON 测试报告
  - 生成可读的 HTML 测试报告
  - 终端实时进度输出

用法:
  # 运行全部测试
  python3 test_runner.py

  # 运行指定测试（逗号分隔）
  python3 test_runner.py --tests test2_create_single_sandbox,test4_concurrent_sandbox_create

  # 列出所有可用测试
  python3 test_runner.py --list

  # 指定输出目录
  python3 test_runner.py --output-dir ./reports
"""
import argparse
import datetime
import html
import json
import os
import socket
import sys
import time
import traceback

# 将脚本所在目录加入 sys.path
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

from test_cases import ALL_TESTS, TemplateNotBuiltError


# ── 数据结构 ──────────────────────────────────────────────────

def make_result(test_id, name, category, status, duration, detail=None, error=None):
    return {
        "test_id": test_id,
        "name": name,
        "category": category,
        "status": status,        # PASS / FAIL / SKIP / ERROR
        "duration_s": round(duration, 2),
        "detail": detail or {},
        "error": error,
    }


# ── 运行单个测试 ─────────────────────────────────────────────

def run_one(test_id, spec):
    name = spec["name"]
    category = spec["category"]
    func = spec["func"]
    print(f"\n{'─' * 60}")
    print(f"▶ [{test_id}] {name}  ({category})")
    print(f"{'─' * 60}")

    t0 = time.time()
    try:
        detail = func()
        duration = time.time() - t0

        # 判断通过与否：如果返回 dict 里显式标记了失败
        failed = False
        if isinstance(detail, dict):
            if detail.get("failed_count", 0) > 0:
                failed = True
            if detail.get("cross_node_verified") is False:
                failed = True
            if detail.get("size_match") is False:
                failed = True

        status = "FAIL" if failed else "PASS"
        icon = "❌" if failed else "✅"
        print(f"{icon} [{test_id}] {status} — {duration:.1f}s")
        return make_result(test_id, name, category, status, duration, detail=detail)

    except TemplateNotBuiltError as exc:
        duration = time.time() - t0
        print(f"⏭️  [{test_id}] SKIP — {duration:.1f}s ({exc})")
        return make_result(test_id, name, category, "SKIP", duration,
                           detail={"reason": str(exc)})

    except Exception as exc:
        duration = time.time() - t0
        tb = traceback.format_exc()
        print(f"❌ [{test_id}] ERROR — {duration:.1f}s")
        print(tb)
        return make_result(test_id, name, category, "ERROR", duration,
                           error={"message": str(exc), "traceback": tb})


# ── 报告生成 ──────────────────────────────────────────────────

def write_json_report(results, meta, path):
    report = {"meta": meta, "results": results}
    with open(path, "w", encoding="utf-8") as f:
        json.dump(report, f, ensure_ascii=False, indent=2)
    print(f"📄 JSON 报告: {path}")


def write_html_report(results, meta, path):
    total = len(results)
    passed = sum(1 for r in results if r["status"] == "PASS")
    failed = sum(1 for r in results if r["status"] == "FAIL")
    errors = sum(1 for r in results if r["status"] == "ERROR")
    skipped = sum(1 for r in results if r["status"] == "SKIP")
    total_time = sum(r["duration_s"] for r in results)

    status_color = {
        "PASS": "#27ae60", "FAIL": "#e74c3c",
        "ERROR": "#e67e22", "SKIP": "#95a5a6",
    }

    rows = []
    for i, r in enumerate(results, 1):
        color = status_color.get(r["status"], "#333")
        detail_str = html.escape(json.dumps(r["detail"], ensure_ascii=False, indent=2)) if r["detail"] else ""
        error_str = ""
        if r["error"]:
            error_str = html.escape(r["error"].get("traceback", r["error"].get("message", "")))

        rows.append(f"""
        <tr>
          <td>{i}</td>
          <td>{html.escape(r['test_id'])}</td>
          <td>{html.escape(r['name'])}</td>
          <td>{html.escape(r['category'])}</td>
          <td style="color:{color};font-weight:bold">{r['status']}</td>
          <td>{r['duration_s']}s</td>
          <td><pre style="margin:0;white-space:pre-wrap;max-width:500px;font-size:12px">{detail_str}</pre></td>
          <td><pre style="margin:0;white-space:pre-wrap;max-width:400px;font-size:12px;color:#c0392b">{error_str}</pre></td>
        </tr>""")

    overall = "PASS" if (failed + errors) == 0 else "FAIL"
    overall_color = "#27ae60" if overall == "PASS" else "#e74c3c"

    doc = f"""<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="UTF-8">
<title>E2B 平台测试报告 — {meta['timestamp']}</title>
<style>
  body {{ font-family: -apple-system, "Segoe UI", Roboto, sans-serif; margin: 20px; background: #f8f9fa; }}
  h1 {{ color: #2c3e50; }}
  .summary {{ display: flex; gap: 20px; margin: 20px 0; flex-wrap: wrap; }}
  .card {{ background: #fff; border-radius: 8px; padding: 16px 24px; box-shadow: 0 1px 3px rgba(0,0,0,.12);
           min-width: 120px; text-align: center; }}
  .card .num {{ font-size: 28px; font-weight: bold; }}
  .card .label {{ font-size: 13px; color: #666; margin-top: 4px; }}
  table {{ border-collapse: collapse; width: 100%; background: #fff; box-shadow: 0 1px 3px rgba(0,0,0,.12);
           border-radius: 8px; overflow: hidden; }}
  th {{ background: #2c3e50; color: #fff; padding: 10px 12px; text-align: left; font-size: 13px; }}
  td {{ padding: 8px 12px; border-bottom: 1px solid #eee; font-size: 13px; vertical-align: top; }}
  tr:hover {{ background: #f1f8ff; }}
  .meta {{ color: #888; font-size: 13px; margin-bottom: 10px; }}
</style>
</head>
<body>
<h1>E2B 平台自动化测试报告</h1>
<div class="meta">
  运行时间: {meta['timestamp']} &nbsp;|&nbsp;
  主机: {html.escape(meta['hostname'])} &nbsp;|&nbsp;
  总耗时: {total_time:.1f}s
</div>

<div class="summary">
  <div class="card"><div class="num" style="color:{overall_color}">{overall}</div><div class="label">整体结果</div></div>
  <div class="card"><div class="num">{total}</div><div class="label">总计</div></div>
  <div class="card"><div class="num" style="color:#27ae60">{passed}</div><div class="label">通过</div></div>
  <div class="card"><div class="num" style="color:#e74c3c">{failed}</div><div class="label">失败</div></div>
  <div class="card"><div class="num" style="color:#e67e22">{errors}</div><div class="label">异常</div></div>
  <div class="card"><div class="num" style="color:#95a5a6">{skipped}</div><div class="label">跳过</div></div>
</div>

<table>
<thead>
<tr><th>#</th><th>测试ID</th><th>测试名称</th><th>分类</th><th>状态</th><th>耗时</th><th>详情</th><th>错误</th></tr>
</thead>
<tbody>
{''.join(rows)}
</tbody>
</table>

<p class="meta" style="margin-top:20px">报告由 test_runner.py 自动生成</p>
</body>
</html>"""

    with open(path, "w", encoding="utf-8") as f:
        f.write(doc)
    print(f"📄 HTML 报告: {path}")


# ── 主逻辑 ────────────────────────────────────────────────────

def main():
    parser = argparse.ArgumentParser(description="E2B 平台统一自动化测试运行器")
    parser.add_argument("--tests", type=str, default=None,
                        help="要运行的测试 ID（逗号分隔），默认全部")
    parser.add_argument("--list", action="store_true", help="列出所有可用测试")
    parser.add_argument("--output-dir", type=str, default=None,
                        help="报告输出目录（默认 ./test_reports）")
    args = parser.parse_args()

    if args.list:
        print(f"\n{'ID':<45} {'名称':<30} {'分类'}")
        print("─" * 90)
        for tid, spec in ALL_TESTS.items():
            print(f"{tid:<45} {spec['name']:<30} {spec['category']}")
        return

    # 确定要运行的测试
    if args.tests:
        test_ids = [t.strip() for t in args.tests.split(",")]
        for tid in test_ids:
            if tid not in ALL_TESTS:
                print(f"未知测试 ID: {tid}", file=sys.stderr)
                print(f"可用: {', '.join(ALL_TESTS.keys())}", file=sys.stderr)
                sys.exit(1)
    else:
        test_ids = [tid for tid, spec in ALL_TESTS.items() if not spec.get("skip")]

    # 输出目录
    now = datetime.datetime.now()
    timestamp = now.strftime("%Y%m%d_%H%M%S")
    output_dir = args.output_dir or os.path.join(
        os.path.dirname(os.path.abspath(__file__)), "test_reports"
    )
    os.makedirs(output_dir, exist_ok=True)

    meta = {
        "timestamp": now.isoformat(timespec="seconds"),
        "hostname": socket.gethostname(),
        "tests_requested": test_ids,
    }

    print("=" * 60)
    print(f"  E2B 自动化测试  |  {len(test_ids)} 个测试用例")
    print(f"  时间: {meta['timestamp']}  主机: {meta['hostname']}")
    print("=" * 60)

    # 逐个运行
    results = []
    template_build_failed = False
    template_dependent = {
        "test2_create_single_sandbox",
        "test4_concurrent_sandbox_create",
        "test5_concurrent_sandbox_network",
        "test6_file_transfer",
        "test7_vepfs_cross_node",
    }
    for tid in test_ids:
        if template_build_failed and tid in template_dependent:
            spec = ALL_TESTS[tid]
            print(f"\n{'─' * 60}")
            print(f"⏭️  [{tid}] SKIP — test1 构建模板失败，依赖模板的测试跳过")
            print(f"{'─' * 60}")
            results.append(make_result(tid, spec["name"], spec["category"], "SKIP", 0.0,
                                       detail={"reason": "test1 构建模板失败，跳过依赖模板的测试"}))
            continue

        result = run_one(tid, ALL_TESTS[tid])
        results.append(result)

        if tid == "test1_build_template_ubuntu" and result["status"] in ("FAIL", "ERROR"):
            template_build_failed = True
            print("\n⚠️  test1 构建模板失败，后续依赖模板的测试将自动跳过")

    # 汇总
    passed = sum(1 for r in results if r["status"] == "PASS")
    failed = sum(1 for r in results if r["status"] in ("FAIL", "ERROR"))
    total = len(results)

    print("\n" + "=" * 60)
    print("  测试结果汇总")
    print("=" * 60)
    for r in results:
        icon = {"PASS": "✅", "FAIL": "❌", "ERROR": "💥", "SKIP": "⏭️"}.get(r["status"], "?")
        print(f"  {icon} {r['test_id']:<45} {r['status']:<6} {r['duration_s']}s")
    print(f"\n  总计: {total}  通过: {passed}  失败: {failed}")
    print("=" * 60)

    # 写报告
    json_path = os.path.join(output_dir, f"report_{timestamp}.json")
    html_path = os.path.join(output_dir, f"report_{timestamp}.html")
    write_json_report(results, meta, json_path)
    write_html_report(results, meta, html_path)

    # 同时写一个 latest 软链接
    for ext, src in [("json", json_path), ("html", html_path)]:
        latest = os.path.join(output_dir, f"report_latest.{ext}")
        try:
            if os.path.islink(latest) or os.path.exists(latest):
                os.remove(latest)
            os.symlink(os.path.basename(src), latest)
        except OSError:
            pass

    sys.exit(1 if failed > 0 else 0)


if __name__ == "__main__":
    main()
