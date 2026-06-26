#!/usr/bin/env python3
from __future__ import annotations

import html
import json
import math
import os
import re
import shutil
import zipfile
from datetime import datetime, timezone
from pathlib import Path

from PIL import Image, ImageDraw, ImageFont


ROOT = Path(__file__).resolve().parents[1]
OUT_DIR = ROOT / "reports" / "snapshot_memory_sharing_report_assets"
PPTX = ROOT / "reports" / "snapshot_memory_sharing_report.pptx"

W, H = 1920, 1080
EMU_W, EMU_H = 12192000, 6858000

FONT_CJK = "/usr/share/fonts/truetype/arphic-gbsn00lp/gbsn00lp.ttf"
FONT_LATIN = "/usr/share/fonts/truetype/lato/Lato-Regular.ttf"
FONT_LATIN_BOLD = "/usr/share/fonts/truetype/lato/Lato-Bold.ttf"

BG = "#F7F8F9"
INK = "#17212B"
MUTED = "#657280"
LINE = "#D9DEE5"
TEAL = "#008C8C"
GREEN = "#2E7D32"
ORANGE = "#D97904"
BLUE = "#2B6CB0"
RED = "#B83232"
PURPLE = "#6750A4"
WHITE = "#FFFFFF"


def font(size: int, bold: bool = False) -> ImageFont.FreeTypeFont:
    path = FONT_LATIN_BOLD if bold and Path(FONT_LATIN_BOLD).exists() else FONT_LATIN
    if not Path(path).exists():
        path = FONT_CJK
    return ImageFont.truetype(path, size)


def cjk_font(size: int) -> ImageFont.FreeTypeFont:
    return ImageFont.truetype(FONT_CJK, size)


def mixed_font(size: int, bold: bool = False) -> ImageFont.FreeTypeFont:
    # Droid Sans Fallback renders Chinese and Latin consistently enough for slide text.
    return cjk_font(size)


def canvas() -> Image.Image:
    return Image.new("RGB", (W, H), BG)


def draw_header(d: ImageDraw.ImageDraw, title: str, section: str | None = None):
    d.text((88, 54), title, fill=INK, font=mixed_font(40))
    if section:
        pill(d, 1515, 50, 320, 46, section, TEAL, WHITE, size=22)
    d.line((88, 118, 1832, 118), fill=LINE, width=2)


def draw_footer(d: ImageDraw.ImageDraw, page: int):
    d.text((88, 1016), "E2B Firecracker Sandbox / Snapshot & Memory Sharing", fill="#88919B", font=font(18))
    d.text((1788, 1016), f"{page:02d}", fill="#88919B", font=font(18))


def rounded_rect(d, box, radius=22, fill=WHITE, outline=LINE, width=2):
    d.rounded_rectangle(box, radius=radius, fill=fill, outline=outline, width=width)


def pill(d, x, y, w, h, text, fill, fg=WHITE, size=24):
    d.rounded_rectangle((x, y, x + w, y + h), radius=h // 2, fill=fill)
    tw = d.textlength(text, font=mixed_font(size))
    d.text((x + (w - tw) / 2, y + (h - size) / 2 - 3), text, fill=fg, font=mixed_font(size))


def wrap_text(text: str, fnt, max_w: int) -> list[str]:
    lines: list[str] = []
    for para in text.split("\n"):
        buf = ""
        tokens = re.findall(r"[A-Za-z0-9_./:+%`'()#-]+|[ \t]+|.", para)
        measurer = ImageDraw.Draw(Image.new("RGB", (1, 1)))
        for tok in tokens:
            if tok.isspace() and not buf:
                continue
            trial = buf + tok
            if measurer.textlength(trial, font=fnt) <= max_w or not buf:
                buf = trial
            else:
                lines.append(buf.rstrip())
                buf = tok.lstrip()
                while measurer.textlength(buf, font=fnt) > max_w and len(buf) > 1:
                    cut = len(buf) - 1
                    while cut > 1 and measurer.textlength(buf[:cut], font=fnt) > max_w:
                        cut -= 1
                    lines.append(buf[:cut])
                    buf = buf[cut:]
        if buf:
            lines.append(buf.rstrip())
    return lines


def text_box(d, xy, text: str, width: int, size: int = 28, fill=INK, line_gap=10, bold=False):
    x, y = xy
    fnt = mixed_font(size) if not bold else mixed_font(size)
    for line in wrap_text(text, fnt, width):
        d.text((x, y), line, fill=fill, font=fnt)
        y += size + line_gap
    return y


def metric_card(d, box, value, label, accent=TEAL, note=None):
    x1, y1, x2, y2 = box
    rounded_rect(d, box, radius=20, fill=WHITE, outline="#E0E5EA")
    d.rectangle((x1, y1, x1 + 8, y2), fill=accent)
    d.text((x1 + 34, y1 + 26), value, fill=accent, font=font(56, bold=True))
    d.text((x1 + 34, y1 + 94), label, fill=INK, font=mixed_font(28))
    if note:
        d.text((x1 + 34, y1 + 132), note, fill=MUTED, font=mixed_font(20))


def tag(d, x, y, text, color=TEAL):
    pill(d, x, y, 168, 40, text, color, WHITE, size=20)


def arrow(d, start, end, color=INK, width=5):
    x1, y1 = start
    x2, y2 = end
    d.line((x1, y1, x2, y2), fill=color, width=width)
    angle = math.atan2(y2 - y1, x2 - x1)
    size = 16
    pts = [
        (x2, y2),
        (x2 - size * math.cos(angle - math.pi / 6), y2 - size * math.sin(angle - math.pi / 6)),
        (x2 - size * math.cos(angle + math.pi / 6), y2 - size * math.sin(angle + math.pi / 6)),
    ]
    d.polygon(pts, fill=color)


def paste_fit(base: Image.Image, img_path: Path, box, border=True):
    img = Image.open(img_path).convert("RGB")
    x1, y1, x2, y2 = box
    max_w, max_h = x2 - x1, y2 - y1
    img.thumbnail((max_w, max_h), Image.LANCZOS)
    x = x1 + (max_w - img.width) // 2
    y = y1 + (max_h - img.height) // 2
    if border:
        d = ImageDraw.Draw(base)
        rounded_rect(d, (x - 12, y - 12, x + img.width + 12, y + img.height + 12), radius=18, fill=WHITE, outline="#E1E6ED")
    base.paste(img, (x, y))


def slide_title(page: int) -> Image.Image:
    im = canvas()
    d = ImageDraw.Draw(im)
    d.rectangle((0, 0, W, H), fill="#F3F5F7")
    d.rectangle((0, 0, W, 20), fill=TEAL)
    d.text((96, 122), "三层快照机制与跨 microVM 内存共享", fill=INK, font=mixed_font(62))
    d.text((100, 214), "组内汇报材料", fill=MUTED, font=mixed_font(34))

    metric_card(d, (96, 390, 520, 570), "315ms", "1 并发平均创建延迟", TEAL, "真实 Firecracker snapshot resume")
    metric_card(d, (560, 390, 984, 570), "369ms", "10 并发平均创建延迟", BLUE, "平均延迟仅较单并发 +17%")
    metric_card(d, (1024, 390, 1448, 570), "100%", "benchmark 成功率", GREEN, "1/2/5/10 并发均通过")

    rounded_rect(d, (96, 666, 1448, 850), radius=24, fill=WHITE, outline="#E0E5EA")
    text_box(d, (136, 704), "核心成果：把 VM 恢复拆成可共享基础层、可共享运行时层和实例私有层；多个 microVM 通过同一 memfile 的 MAP_PRIVATE 映射共享只读物理页，写入走 CoW 隔离。", 1250, 34, fill=INK, line_gap=14)
    d.text((100, 936), "2026-06-26", fill=MUTED, font=font(24))
    draw_footer(d, page)
    return im


def slide_goal(page: int) -> Image.Image:
    im = canvas()
    d = ImageDraw.Draw(im)
    draw_header(d, "目标与收益", "WHY")

    items = [
        ("冷启动", "跳过 kernel/envd 启动，把真实 FC 创建控制在 300ms 级"),
        ("内存占用", "跨 VM 共享 L0/L1 只读页，实例只保存 L2 dirty delta"),
        ("并发稳定", "减少重复 page fault 和 memfile 复制，高并发下 tail 不线性恶化"),
    ]
    x = 110
    for i, (t, body) in enumerate(items):
        bx = (x + i * 585, 190, x + i * 585 + 500, 410)
        rounded_rect(d, bx, radius=24, fill=WHITE, outline="#DCE3EA")
        d.text((bx[0] + 34, bx[1] + 28), t, fill=[TEAL, BLUE, ORANGE][i], font=mixed_font(42))
        text_box(d, (bx[0] + 34, bx[1] + 102), body, 420, 27, fill=INK, line_gap=10)

    d.text((120, 520), "汇报口径", fill=INK, font=mixed_font(38))
    d.line((120, 585, 1780, 585), fill=LINE, width=2)
    steps = [
        ("API/SDK", "创建请求"),
        ("Orchestrator", "分配资源"),
        ("Template Cache", "命中快照"),
        ("Firecracker", "load snapshot"),
        ("envd", "ready"),
    ]
    sx = 130
    for i, (a, b) in enumerate(steps):
        x1 = sx + i * 340
        rounded_rect(d, (x1, 660, x1 + 250, 785), radius=18, fill=WHITE, outline="#DCE3EA")
        d.text((x1 + 24, 685), a, fill=INK, font=font(28, bold=True))
        d.text((x1 + 24, 730), b, fill=MUTED, font=mixed_font(24))
        if i < len(steps) - 1:
            arrow(d, (x1 + 260, 722), (x1 + 315, 722), TEAL, width=4)

    d.text((120, 900), "本次 benchmark 测的是 “真实 Firecracker VM 从 snapshot 创建到 ready”，不是 dummy orchestrator。", fill=MUTED, font=mixed_font(26))
    draw_footer(d, page)
    return im


def slide_core(page: int) -> Image.Image:
    im = canvas()
    d = ImageDraw.Draw(im)
    draw_header(d, "核心机制", "WHAT")

    # Three pillars.
    pillars = [
        ("1", "三层快照", "L0 infrastructure\nL1 runtime/tool\nL2 instance delta", TEAL),
        ("2", "共享 memfile", "MAP_PRIVATE mmap\nhost page cache dedupe\nref-count 管理", BLUE),
        ("3", "CoW 私有层", "dirty page bitmap\nsparse overlay\nexit checkpoint", ORANGE),
    ]
    for i, (num, title, body, color) in enumerate(pillars):
        x = 130 + i * 575
        rounded_rect(d, (x, 188, x + 475, 520), radius=26, fill=WHITE, outline="#DCE3EA")
        d.ellipse((x + 36, 226, x + 98, 288), fill=color)
        d.text((x + 56, 237), num, fill=WHITE, font=font(30, bold=True))
        d.text((x + 122, 228), title, fill=INK, font=mixed_font(40))
        text_box(d, (x + 46, 330), body, 380, 30, fill=MUTED, line_gap=18)

    # Outcome strip.
    rounded_rect(d, (130, 642, 1790, 862), radius=24, fill="#FFFFFF", outline="#DCE3EA")
    d.text((180, 684), "效果链路", fill=INK, font=mixed_font(36))
    effects = [
        ("共享公共状态", TEAL),
        ("减少重复恢复", BLUE),
        ("写入自动隔离", ORANGE),
        ("退出保存增量", GREEN),
    ]
    for i, (label, color) in enumerate(effects):
        cx = 465 + i * 320
        pill(d, cx - 120, 746, 240, 56, label, color, WHITE, size=24)
        if i < len(effects) - 1:
            arrow(d, (cx + 132, 774), (cx + 178, 774), "#8B96A3", width=4)

    d.text((135, 930), "原则：共享只读基础状态；任何用户态/实例态写入都落到私有层。", fill=MUTED, font=mixed_font(28))
    draw_footer(d, page)
    return im


def slide_layers(page: int) -> Image.Image:
    im = canvas()
    d = ImageDraw.Draw(im)
    draw_header(d, "三层快照架构", "ARCH")

    layers = [
        ("L0", "Infrastructure", "kernel / init / envd / base system", TEAL, 210),
        ("L1", "Runtime", "Python / Node / tool bridge / warmed runtime", BLUE, 405),
        ("L2", "Instance", "workspace / process delta / dirty pages", ORANGE, 600),
    ]
    for label, name, desc, color, y in layers:
        rounded_rect(d, (180, y, 1020, y + 130), radius=18, fill=WHITE, outline="#DCE3EA")
        d.rectangle((180, y, 198, y + 130), fill=color)
        d.text((230, y + 20), label, fill=color, font=font(48, bold=True))
        d.text((350, y + 22), name, fill=INK, font=font(34, bold=True))
        d.text((350, y + 78), desc, fill=MUTED, font=font(24))
        if label != "L2":
            pill(d, 850, y + 38, 130, 45, "shared", color, WHITE, size=20)
        else:
            pill(d, 850, y + 38, 130, 45, "private", color, WHITE, size=20)

    for y in [340, 535]:
        arrow(d, (600, y), (600, y + 50), "#9AA5B1", width=4)

    # VM fan-out.
    for i in range(3):
        x = 1210 + i * 190
        rounded_rect(d, (x, 248, x + 150, 420), radius=20, fill=WHITE, outline="#DCE3EA")
        d.rectangle((x + 30, 285, x + 120, 335), fill="#EAF6F6")
        d.text((x + 38, 296), f"VM {i+1}", fill=INK, font=font(22, bold=True))
        d.text((x + 34, 360), "L0/L1\nshared", fill=MUTED, font=font(20))
    bus_y = 210
    arrow(d, (1030, 475), (1140, bus_y), TEAL, width=5)
    d.line((1140, bus_y, 1665, bus_y), fill=TEAL, width=5)
    for x in [1210, 1400, 1590]:
        arrow(d, (x + 75, bus_y), (x + 75, 245), TEAL, width=4)

    rounded_rect(d, (1180, 575, 1755, 830), radius=24, fill=WHITE, outline="#DCE3EA")
    d.text((1220, 614), "兼容策略", fill=INK, font=mixed_font(34))
    text_box(d, (1220, 675), "非 layered template 继续走原有 snapshot resume；layered resume 失败自动 fallback 到普通 snapshot / cold boot。", 480, 26, fill=MUTED, line_gap=9)

    draw_footer(d, page)
    return im


def slide_resume(page: int) -> Image.Image:
    im = canvas()
    d = ImageDraw.Draw(im)
    draw_header(d, "恢复路径", "FLOW")

    nodes = [
        ("Create", "API/gRPC request", 120, 265, TEAL),
        ("Resolve", "template + metadata", 410, 265, BLUE),
        ("Map", "L0/L1 shared memfile", 700, 265, PURPLE),
        ("Merge", "pre-merged memfile", 990, 265, ORANGE),
        ("Load", "Firecracker snapshot", 1280, 265, GREEN),
        ("Ready", "envd accepts traffic", 1570, 265, TEAL),
    ]
    for i, (title, sub, x, y, color) in enumerate(nodes):
        rounded_rect(d, (x, y, x + 220, y + 135), radius=20, fill=WHITE, outline="#DCE3EA")
        d.text((x + 24, y + 28), title, fill=color, font=font(32, bold=True))
        text_box(d, (x + 24, y + 78), sub, 170, 22, fill=MUTED, line_gap=6)
        if i < len(nodes) - 1:
            arrow(d, (x + 230, y + 68), (x + 280, y + 68), "#8C98A4", width=4)

    rounded_rect(d, (180, 560, 850, 800), radius=24, fill=WHITE, outline="#DCE3EA")
    d.text((220, 602), "关键实现", fill=INK, font=mixed_font(34))
    text_box(d, (220, 660), "`createWithLayeredSnapshot` 尝试三层恢复；`buildMemoryLayers` 负责共享层映射；`loadLayeredSnapshot` 把多层 memfile 合成 Firecracker 可加载的单文件。", 580, 26, fill=MUTED, line_gap=10)

    rounded_rect(d, (1010, 560, 1680, 800), radius=24, fill=WHITE, outline="#DCE3EA")
    d.text((1050, 602), "并行优化", fill=INK, font=mixed_font(34))
    text_box(d, (1050, 660), "恢复后后台 prefetch L0/L1 热区；退出时可导入 dirty pages 并保存 L2 checkpoint，为下一次 resume 减少重复工作。", 580, 26, fill=MUTED, line_gap=10)

    draw_footer(d, page)
    return im


def slide_memory(page: int) -> Image.Image:
    im = canvas()
    d = ImageDraw.Draw(im)
    draw_header(d, "跨 microVM 内存共享", "MEM")

    # Shared file and page cache.
    rounded_rect(d, (120, 210, 510, 760), radius=24, fill=WHITE, outline="#DCE3EA")
    d.text((160, 250), "shared memfile", fill=TEAL, font=font(36, bold=True))
    for i in range(7):
        y = 330 + i * 50
        fill = ["#D7F0ED", "#D7F0ED", "#EAF0F7", "#D7F0ED", "#EAF0F7", "#D7F0ED", "#EAF0F7"][i]
        d.rounded_rectangle((170, y, 460, y + 32), radius=8, fill=fill, outline="#BFD4D5")
    d.text((165, 700), "L0/L1 只读页", fill=MUTED, font=mixed_font(26))

    # Three VMs.
    bus_y = 176
    arrow(d, (510, 330), (690, bus_y), TEAL, width=4)
    d.line((690, bus_y, 1475, bus_y), fill=TEAL, width=4)
    for i, color in enumerate([BLUE, ORANGE, GREEN]):
        x = 760 + i * 300
        rounded_rect(d, (x, 215, x + 230, 420), radius=22, fill=WHITE, outline="#DCE3EA")
        d.text((x + 55, 255), f"microVM {i+1}", fill=color, font=font(30, bold=True))
        d.text((x + 48, 315), "MAP_PRIVATE", fill=INK, font=font(25, bold=True))
        d.text((x + 55, 358), "read shared\nwrite CoW", fill=MUTED, font=font(21))
        arrow(d, (x + 115, bus_y), (x + 115, 210), color, width=4)

        rounded_rect(d, (x, 560, x + 230, 710), radius=20, fill="#FFF8EF", outline="#F3CF9E")
        d.text((x + 34, 594), "L2 overlay", fill=ORANGE, font=font(28, bold=True))
        d.text((x + 34, 642), "dirty bitmap\nsparse data", fill=MUTED, font=font(20))
        arrow(d, (x + 115, 430), (x + 115, 555), ORANGE, width=4)

    rounded_rect(d, (125, 835, 1788, 930), radius=18, fill=WHITE, outline="#DCE3EA")
    d.text((165, 865), "结果：公共页只在 host page cache 中保留一份；每个 VM 只为写入页付费，隔离语义由 CoW 保证。", fill=INK, font=mixed_font(30))
    draw_footer(d, page)
    return im


def slide_method(page: int) -> Image.Image:
    im = canvas()
    d = ImageDraw.Draw(im)
    draw_header(d, "Benchmark 口径", "TEST")

    left = (120, 190, 900, 840)
    right = (990, 190, 1788, 840)
    rounded_rect(d, left, radius=24, fill=WHITE, outline="#DCE3EA")
    rounded_rect(d, right, radius=24, fill=WHITE, outline="#DCE3EA")

    d.text((165, 230), "环境", fill=INK, font=mixed_font(38))
    rows = [
        ("CPU", "Intel Xeon E5-2660 v3 @ 2.60GHz / 40 cores"),
        ("RAM", "157 GiB"),
        ("OS", "Linux 6.8.0-124-generic"),
        ("VM", "Firecracker v1.12.1 / 2 vCPU / 512 MB RAM / 2 GB disk"),
        ("Template", "e2bdev/base, snapshot-based resume"),
    ]
    y = 305
    for k, v in rows:
        d.text((165, y), k, fill=TEAL, font=font(25, bold=True))
        text_box(d, (300, y), v, 520, 24, fill=INK, line_gap=5)
        y += 82

    d.text((1035, 230), "方法", fill=INK, font=mixed_font(38))
    bullets = [
        "1/2/5/10 并发，每档 10 次迭代",
        "每次并发调用 ResumeSandbox()",
        "计时范围覆盖 NBD/rootfs/network/FC resume/envd ready",
        "每轮结束清理 sandbox，warm-up 不计入结果",
        "对比对象为 CubeSandbox 公开结果",
    ]
    y = 312
    for b in bullets:
        d.ellipse((1038, y + 9, 1050, y + 21), fill=BLUE)
        text_box(d, (1070, y), b, 620, 28, fill=INK, line_gap=7)
        y += 84

    pill(d, 1040, 740, 430, 58, "真实 Firecracker，不是 dummy path", RED, WHITE, size=24)
    draw_footer(d, page)
    return im


def slide_chart_avg(page: int) -> Image.Image:
    im = canvas()
    d = ImageDraw.Draw(im)
    draw_header(d, "结果 1：平均延迟", "RESULT")
    paste_fit(im, ROOT / "packages/orchestrator/benchmarks/chart_avg_latency_bars.png", (90, 158, 1300, 900))
    metric_card(d, (1340, 190, 1785, 350), "315ms", "E2B / 1 并发 avg", TEAL)
    metric_card(d, (1340, 390, 1785, 550), "369ms", "E2B / 10 并发 avg", BLUE)
    metric_card(d, (1340, 590, 1785, 750), "0 fail", "所有并发档失败数", GREEN)
    d.text((1340, 820), "单并发略慢于对比系统；并发上来后平均延迟保持稳定。", fill=MUTED, font=mixed_font(25))
    draw_footer(d, page)
    return im


def slide_chart_scaling(page: int) -> Image.Image:
    im = canvas()
    d = ImageDraw.Draw(im)
    draw_header(d, "结果 2：并发扩展", "RESULT")
    paste_fit(im, ROOT / "packages/orchestrator/benchmarks/chart_scaling_overlay.png", (90, 160, 960, 860))
    paste_fit(im, ROOT / "packages/orchestrator/benchmarks/chart_fairness_comparison.png", (1010, 160, 1830, 860))
    rounded_rect(d, (230, 890, 1690, 960), radius=18, fill=WHITE, outline="#DCE3EA")
    d.text((270, 910), "10 并发下：E2B avg 369ms；CubeSandbox avg 864ms。E2B/Cube 延迟比约 0.43x。", fill=INK, font=mixed_font(28))
    draw_footer(d, page)
    return im


def slide_chart_tail(page: int) -> Image.Image:
    im = canvas()
    d = ImageDraw.Draw(im)
    draw_header(d, "结果 3：尾延迟", "RESULT")
    paste_fit(im, ROOT / "packages/orchestrator/benchmarks/chart_percentile_lines.png", (90, 155, 1400, 890))
    metric_card(d, (1435, 230, 1790, 385), "466ms", "10 并发 p99", ORANGE)
    metric_card(d, (1435, 430, 1790, 585), "436ms", "10 并发 p95", BLUE)
    metric_card(d, (1435, 630, 1790, 785), "375ms", "10 并发 p50", TEAL)
    d.text((1435, 850), "尾部仍在 500ms 内，可继续通过 prefetch/capsule 降低。", fill=MUTED, font=mixed_font(24))
    draw_footer(d, page)
    return im


def slide_status(page: int) -> Image.Image:
    im = canvas()
    d = ImageDraw.Draw(im)
    draw_header(d, "落地状态与下一步", "NEXT")

    cols = [
        ("已完成", GREEN, [
            "LayeredTemplate / LayeredSnapshot 元数据",
            "SharedMemfileManager：跨 VM MAP_PRIVATE 共享",
            "ResumeFromLayeredSnapshot：接入 FC 恢复路径",
            "CoWOverlay：dirty bitmap + sparse checkpoint",
            "真实 Firecracker benchmark 与图表",
        ]),
        ("继续推进", ORANGE, [
            "first-tool 端到端 benchmark",
            "profile seed scrub / taint 校验",
            "intent-driven memory/rootfs prefetch",
            "launch capsule：network/NBD/cgroup 资源预组装",
            "线上 metrics dashboard 与 feature flag 灰度",
        ]),
    ]
    for i, (title, color, items) in enumerate(cols):
        x = 125 + i * 870
        rounded_rect(d, (x, 190, x + 790, 835), radius=24, fill=WHITE, outline="#DCE3EA")
        d.text((x + 42, 236), title, fill=color, font=mixed_font(42))
        y = 330
        for item in items:
            d.rounded_rectangle((x + 46, y + 8, x + 72, y + 34), radius=6, fill=color)
            d.text((x + 95, y), item, fill=INK, font=mixed_font(28))
            y += 86

    rounded_rect(d, (130, 895, 1785, 960), radius=18, fill="#FFFFFF", outline="#DCE3EA")
    d.text((170, 912), "汇报结论：机制已打通，真实 VM 创建保持 300ms 级；下一阶段重点从 sandbox ready 推进到 first tool ready。", fill=INK, font=mixed_font(28))
    draw_footer(d, page)
    return im


def render_slides() -> list[Path]:
    if OUT_DIR.exists():
        shutil.rmtree(OUT_DIR)
    OUT_DIR.mkdir(parents=True, exist_ok=True)
    slide_fns = [
        slide_title,
        slide_goal,
        slide_core,
        slide_layers,
        slide_resume,
        slide_memory,
        slide_method,
        slide_chart_avg,
        slide_chart_scaling,
        slide_chart_tail,
        slide_status,
    ]
    paths = []
    for i, fn in enumerate(slide_fns, 1):
        im = fn(i)
        p = OUT_DIR / f"slide{i:02d}.png"
        im.save(p, optimize=True)
        paths.append(p)
    return paths


def rels_xml(entries: list[tuple[str, str, str]]) -> str:
    body = "\n".join(
        f'  <Relationship Id="{rid}" Type="{typ}" Target="{html.escape(target)}"/>'
        for rid, typ, target in entries
    )
    return f'<?xml version="1.0" encoding="UTF-8" standalone="yes"?>\n<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">\n{body}\n</Relationships>'


def content_types(slide_count: int) -> str:
    overrides = [
        ('/ppt/presentation.xml', 'application/vnd.openxmlformats-officedocument.presentationml.presentation.main+xml'),
        ('/ppt/slideMasters/slideMaster1.xml', 'application/vnd.openxmlformats-officedocument.presentationml.slideMaster+xml'),
        ('/ppt/slideLayouts/slideLayout1.xml', 'application/vnd.openxmlformats-officedocument.presentationml.slideLayout+xml'),
        ('/ppt/theme/theme1.xml', 'application/vnd.openxmlformats-officedocument.theme+xml'),
        ('/docProps/core.xml', 'application/vnd.openxmlformats-package.core-properties+xml'),
        ('/docProps/app.xml', 'application/vnd.openxmlformats-officedocument.extended-properties+xml'),
    ]
    for i in range(1, slide_count + 1):
        overrides.append((f'/ppt/slides/slide{i}.xml', 'application/vnd.openxmlformats-officedocument.presentationml.slide+xml'))
    body = '\n'.join(f'  <Override PartName="{p}" ContentType="{ct}"/>' for p, ct in overrides)
    return f'''<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
  <Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
  <Default Extension="xml" ContentType="application/xml"/>
  <Default Extension="png" ContentType="image/png"/>
{body}
</Types>'''


def presentation_xml(slide_count: int) -> str:
    ids = '\n'.join(f'    <p:sldId id="{255+i}" r:id="rId{i+1}"/>' for i in range(1, slide_count + 1))
    return f'''<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<p:presentation xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main">
  <p:sldMasterIdLst>
    <p:sldMasterId id="2147483648" r:id="rId1"/>
  </p:sldMasterIdLst>
  <p:sldIdLst>
{ids}
  </p:sldIdLst>
  <p:sldSz cx="{EMU_W}" cy="{EMU_H}" type="wide"/>
  <p:notesSz cx="6858000" cy="9144000"/>
  <p:defaultTextStyle>
    <a:defPPr><a:defRPr lang="zh-CN"/></a:defPPr>
  </p:defaultTextStyle>
</p:presentation>'''


def slide_xml(idx: int) -> str:
    return f'''<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<p:sld xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main">
  <p:cSld>
    <p:spTree>
      <p:nvGrpSpPr>
        <p:cNvPr id="1" name=""/>
        <p:cNvGrpSpPr/>
        <p:nvPr/>
      </p:nvGrpSpPr>
      <p:grpSpPr>
        <a:xfrm>
          <a:off x="0" y="0"/>
          <a:ext cx="0" cy="0"/>
          <a:chOff x="0" y="0"/>
          <a:chExt cx="0" cy="0"/>
        </a:xfrm>
      </p:grpSpPr>
      <p:pic>
        <p:nvPicPr>
          <p:cNvPr id="2" name="slide{idx:02d}.png"/>
          <p:cNvPicPr/>
          <p:nvPr/>
        </p:nvPicPr>
        <p:blipFill>
          <a:blip r:embed="rId2"/>
          <a:stretch><a:fillRect/></a:stretch>
        </p:blipFill>
        <p:spPr>
          <a:xfrm>
            <a:off x="0" y="0"/>
            <a:ext cx="{EMU_W}" cy="{EMU_H}"/>
          </a:xfrm>
          <a:prstGeom prst="rect"><a:avLst/></a:prstGeom>
        </p:spPr>
      </p:pic>
    </p:spTree>
  </p:cSld>
  <p:clrMapOvr><a:masterClrMapping/></p:clrMapOvr>
</p:sld>'''


def slide_master_xml() -> str:
    return '''<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<p:sldMaster xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main">
  <p:cSld><p:spTree><p:nvGrpSpPr><p:cNvPr id="1" name=""/><p:cNvGrpSpPr/><p:nvPr/></p:nvGrpSpPr><p:grpSpPr><a:xfrm><a:off x="0" y="0"/><a:ext cx="0" cy="0"/><a:chOff x="0" y="0"/><a:chExt cx="0" cy="0"/></a:xfrm></p:grpSpPr></p:spTree></p:cSld>
  <p:clrMap bg1="lt1" tx1="dk1" bg2="lt2" tx2="dk2" accent1="accent1" accent2="accent2" accent3="accent3" accent4="accent4" accent5="accent5" accent6="accent6" hlink="hlink" folHlink="folHlink"/>
  <p:sldLayoutIdLst><p:sldLayoutId id="2147483649" r:id="rId1"/></p:sldLayoutIdLst>
  <p:txStyles><p:titleStyle/><p:bodyStyle/><p:otherStyle/></p:txStyles>
</p:sldMaster>'''


def slide_layout_xml() -> str:
    return '''<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<p:sldLayout xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" type="blank" preserve="1">
  <p:cSld name="Blank"><p:spTree><p:nvGrpSpPr><p:cNvPr id="1" name=""/><p:cNvGrpSpPr/><p:nvPr/></p:nvGrpSpPr><p:grpSpPr><a:xfrm><a:off x="0" y="0"/><a:ext cx="0" cy="0"/><a:chOff x="0" y="0"/><a:chExt cx="0" cy="0"/></a:xfrm></p:grpSpPr></p:spTree></p:cSld>
  <p:clrMapOvr><a:masterClrMapping/></p:clrMapOvr>
</p:sldLayout>'''


def theme_xml() -> str:
    return '''<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<a:theme xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" name="SnapshotReport">
  <a:themeElements>
    <a:clrScheme name="SnapshotReport">
      <a:dk1><a:srgbClr val="17212B"/></a:dk1>
      <a:lt1><a:srgbClr val="F7F8F9"/></a:lt1>
      <a:dk2><a:srgbClr val="657280"/></a:dk2>
      <a:lt2><a:srgbClr val="FFFFFF"/></a:lt2>
      <a:accent1><a:srgbClr val="008C8C"/></a:accent1>
      <a:accent2><a:srgbClr val="2B6CB0"/></a:accent2>
      <a:accent3><a:srgbClr val="D97904"/></a:accent3>
      <a:accent4><a:srgbClr val="2E7D32"/></a:accent4>
      <a:accent5><a:srgbClr val="6750A4"/></a:accent5>
      <a:accent6><a:srgbClr val="B83232"/></a:accent6>
      <a:hlink><a:srgbClr val="2B6CB0"/></a:hlink>
      <a:folHlink><a:srgbClr val="6750A4"/></a:folHlink>
    </a:clrScheme>
    <a:fontScheme name="Office">
      <a:majorFont><a:latin typeface="Lato"/><a:ea typeface="Droid Sans Fallback"/></a:majorFont>
      <a:minorFont><a:latin typeface="Lato"/><a:ea typeface="Droid Sans Fallback"/></a:minorFont>
    </a:fontScheme>
    <a:fmtScheme name="Office"><a:fillStyleLst/><a:lnStyleLst/><a:effectStyleLst/><a:bgFillStyleLst/></a:fmtScheme>
  </a:themeElements>
</a:theme>'''


def core_xml() -> str:
    now = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
    return f'''<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<cp:coreProperties xmlns:cp="http://schemas.openxmlformats.org/package/2006/metadata/core-properties" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:dcterms="http://purl.org/dc/terms/" xmlns:dcmitype="http://purl.org/dc/dcmitype/" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance">
  <dc:title>三层快照机制与跨 microVM 内存共享</dc:title>
  <dc:subject>Snapshot and memory sharing report</dc:subject>
  <dc:creator>Codex</dc:creator>
  <cp:lastModifiedBy>Codex</cp:lastModifiedBy>
  <dcterms:created xsi:type="dcterms:W3CDTF">{now}</dcterms:created>
  <dcterms:modified xsi:type="dcterms:W3CDTF">{now}</dcterms:modified>
</cp:coreProperties>'''


def app_xml(slide_count: int) -> str:
    return f'''<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Properties xmlns="http://schemas.openxmlformats.org/officeDocument/2006/extended-properties" xmlns:vt="http://schemas.openxmlformats.org/officeDocument/2006/docPropsVTypes">
  <Application>Codex</Application>
  <PresentationFormat>On-screen Show (16:9)</PresentationFormat>
  <Slides>{slide_count}</Slides>
  <ScaleCrop>false</ScaleCrop>
  <Company>E2B</Company>
</Properties>'''


def build_pptx(slides: list[Path]):
    PPTX.parent.mkdir(parents=True, exist_ok=True)
    with zipfile.ZipFile(PPTX, "w", compression=zipfile.ZIP_DEFLATED) as z:
        z.writestr("[Content_Types].xml", content_types(len(slides)))
        z.writestr("_rels/.rels", rels_xml([
            ("rId1", "http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument", "ppt/presentation.xml"),
            ("rId2", "http://schemas.openxmlformats.org/package/2006/relationships/metadata/core-properties", "docProps/core.xml"),
            ("rId3", "http://schemas.openxmlformats.org/officeDocument/2006/relationships/extended-properties", "docProps/app.xml"),
        ]))
        z.writestr("docProps/core.xml", core_xml())
        z.writestr("docProps/app.xml", app_xml(len(slides)))

        z.writestr("ppt/presentation.xml", presentation_xml(len(slides)))
        pres_rels = [("rId1", "http://schemas.openxmlformats.org/officeDocument/2006/relationships/slideMaster", "slideMasters/slideMaster1.xml")]
        for i in range(1, len(slides) + 1):
            pres_rels.append((f"rId{i+1}", "http://schemas.openxmlformats.org/officeDocument/2006/relationships/slide", f"slides/slide{i}.xml"))
        z.writestr("ppt/_rels/presentation.xml.rels", rels_xml(pres_rels))

        z.writestr("ppt/slideMasters/slideMaster1.xml", slide_master_xml())
        z.writestr("ppt/slideMasters/_rels/slideMaster1.xml.rels", rels_xml([
            ("rId1", "http://schemas.openxmlformats.org/officeDocument/2006/relationships/slideLayout", "../slideLayouts/slideLayout1.xml"),
            ("rId2", "http://schemas.openxmlformats.org/officeDocument/2006/relationships/theme", "../theme/theme1.xml"),
        ]))
        z.writestr("ppt/slideLayouts/slideLayout1.xml", slide_layout_xml())
        z.writestr("ppt/slideLayouts/_rels/slideLayout1.xml.rels", rels_xml([
            ("rId1", "http://schemas.openxmlformats.org/officeDocument/2006/relationships/slideMaster", "../slideMasters/slideMaster1.xml"),
        ]))
        z.writestr("ppt/theme/theme1.xml", theme_xml())

        for i, slide in enumerate(slides, 1):
            z.writestr(f"ppt/slides/slide{i}.xml", slide_xml(i))
            z.writestr(f"ppt/slides/_rels/slide{i}.xml.rels", rels_xml([
                ("rId1", "http://schemas.openxmlformats.org/officeDocument/2006/relationships/slideLayout", "../slideLayouts/slideLayout1.xml"),
                ("rId2", "http://schemas.openxmlformats.org/officeDocument/2006/relationships/image", f"../media/slide{i:02d}.png"),
            ]))
            z.write(slide, f"ppt/media/slide{i:02d}.png")


def main():
    slides = render_slides()
    build_pptx(slides)
    print(PPTX)
    print(json.dumps({"slides": len(slides), "assets": str(OUT_DIR)}, ensure_ascii=False))


if __name__ == "__main__":
    main()
