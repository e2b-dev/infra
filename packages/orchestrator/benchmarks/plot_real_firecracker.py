#!/usr/bin/env python3
"""Generate comparison charts for E2B Real Firecracker vs CubeSandbox benchmark."""

import matplotlib
matplotlib.use('Agg')
import matplotlib.pyplot as plt
import matplotlib.ticker as ticker
import numpy as np

# ── Data ───────────────────────────────────────────────────────────────

CONCURRENCIES = [1, 2, 5, 10]

# E2B Real Firecracker VM (from benchmark, 2026-06-11)
e2b = {
    1:  {"avg": 315, "p50": 322, "p95": 336, "p99": 336, "min": 269, "max": 346},
    2:  {"avg": 319, "p50": 322, "p95": 351, "p99": 351, "min": 271, "max": 397},
    5:  {"avg": 289, "p50": 286, "p95": 331, "p99": 337, "min": 219, "max": 356},
    10: {"avg": 369, "p50": 375, "p95": 436, "p99": 466, "min": 224, "max": 466},
}

# CubeSandbox (from previous benchmark, same hardware)
cube = {
    1:  {"avg": 258, "p95": 307, "max": 325},
    5:  {"avg": 459, "p95": 753, "max": 793},
    10: {"avg": 864, "p95": 1414, "max": 1417},
}

# E2B Dummy Orchestrator (previous unfair benchmark)
e2b_dummy = {"avg": 28, "p95": 77, "max": 77}

# Color palette
E2B_COLOR = '#FF5722'       # orange-red
CUBE_COLOR = '#2196F3'      # blue
DUMMY_COLOR = '#9E9E9E'     # grey
E2B_LIGHT = '#FFAB91'
CUBE_LIGHT = '#90CAF9'


def fmt_ms(val):
    if val >= 1000:
        return f"{val/1000:.1f}s"
    return f"{val:.0f}ms"


OUTPUT_DIR = "/users/liufy/Experiment/My-E2B/infra/packages/orchestrator/benchmarks"


# ── Chart 1: Average Latency Comparison (Bar Chart) ────────────────────

def plot_avg_latency_bars():
    fig, ax = plt.subplots(figsize=(11, 6.5))

    concurrencies = [1, 5, 10]
    x = np.arange(len(concurrencies))
    w = 0.32

    e2b_avgs = [e2b[c]["avg"] for c in concurrencies]
    cube_avgs = [cube[c]["avg"] for c in concurrencies]

    bars1 = ax.bar(x - w/2, e2b_avgs, w, label='E2B (Real Firecracker)',
                   color=E2B_COLOR, edgecolor='white', linewidth=0.8,
                   zorder=3)
    bars2 = ax.bar(x + w/2, cube_avgs, w, label='CubeSandbox',
                   color=CUBE_COLOR, edgecolor='white', linewidth=0.8,
                   zorder=3)

    # Value labels
    for bar, val in zip(bars1, e2b_avgs):
        ax.text(bar.get_x() + bar.get_width()/2, bar.get_height() + 15,
                fmt_ms(val), ha='center', va='bottom', fontsize=11,
                fontweight='bold', color=E2B_COLOR)
    for bar, val in zip(bars2, cube_avgs):
        ax.text(bar.get_x() + bar.get_width()/2, bar.get_height() + 15,
                fmt_ms(val), ha='center', va='bottom', fontsize=11,
                fontweight='bold', color=CUBE_COLOR)

    # Ratio annotations
    for i, c in enumerate(concurrencies):
        ratio = e2b_avgs[i] / cube_avgs[i]
        mid_y = max(e2b_avgs[i], cube_avgs[i]) + 60
        if ratio > 1:
            ax.annotate(f'E2B {ratio:.1f}x slower', (x[i], mid_y),
                        ha='center', fontsize=9, color='#D32F2F', style='italic')
        else:
            ax.annotate(f'E2B {1/ratio:.1f}x faster', (x[i], mid_y),
                        ha='center', fontsize=9, color='#2E7D32', style='italic')

    ax.set_xlabel('Concurrency', fontsize=13)
    ax.set_ylabel('Average Latency (ms)', fontsize=13)
    ax.set_title('Sandbox Creation Latency — Real Firecracker vs CubeSandbox',
                 fontsize=15, fontweight='bold', pad=15)
    ax.set_xticks(x)
    ax.set_xticklabels([str(c) for c in concurrencies], fontsize=12)
    ax.legend(fontsize=11, loc='upper left')
    ax.grid(axis='y', alpha=0.3, zorder=0)
    ax.set_axisbelow(True)
    ax.set_ylim(0, max(max(cube_avgs), max(e2b_avgs)) * 1.25)

    fig.tight_layout()
    fig.savefig(f'{OUTPUT_DIR}/chart_avg_latency_bars.png', dpi=150, bbox_inches='tight')
    plt.close(fig)
    print("  Saved chart_avg_latency_bars.png")


# ── Chart 2: Percentile Distribution (Line Chart) ──────────────────────

def plot_percentile_lines():
    fig, axes = plt.subplots(1, 2, figsize=(14, 6))

    concurrencies = [1, 2, 5, 10]

    # E2B percentiles
    ax = axes[0]
    e2b_p50 = [e2b[c]["p50"] for c in concurrencies]
    e2b_p95 = [e2b[c]["p95"] for c in concurrencies]
    e2b_p99 = [e2b[c]["p99"] for c in concurrencies]
    e2b_min = [e2b[c]["min"] for c in concurrencies]
    e2b_max = [e2b[c]["max"] for c in concurrencies]

    ax.fill_between(concurrencies, e2b_min, e2b_max, alpha=0.15, color=E2B_COLOR, label='min–max')
    ax.plot(concurrencies, e2b_p50, 'o-', color=E2B_COLOR, linewidth=2.5,
            markersize=8, label='p50', markerfacecolor='white', markeredgewidth=2)
    ax.plot(concurrencies, e2b_p95, 's--', color=E2B_COLOR, linewidth=2,
            markersize=7, label='p95', markerfacecolor='white', markeredgewidth=2)
    ax.plot(concurrencies, e2b_p99, '^:', color=E2B_COLOR, linewidth=1.5,
            markersize=7, label='p99', markerfacecolor='white', markeredgewidth=2)

    for c, v in zip(concurrencies, e2b_p50):
        ax.annotate(fmt_ms(v), (c, v), textcoords="offset points",
                    xytext=(0, 12), ha='center', fontsize=9, fontweight='bold', color=E2B_COLOR)

    ax.set_xlabel('Concurrency', fontsize=12)
    ax.set_ylabel('Latency (ms)', fontsize=12)
    ax.set_title('E2B (Real Firecracker)', fontsize=14, fontweight='bold', color=E2B_COLOR)
    ax.set_xticks(concurrencies)
    ax.legend(fontsize=9, loc='upper left')
    ax.grid(alpha=0.3)
    ax.set_ylim(150, 550)

    # CubeSandbox percentiles
    ax = axes[1]
    cube_c = [1, 5, 10]
    cube_avgs = [cube[c]["avg"] for c in cube_c]
    cube_p95 = [cube[c]["p95"] for c in cube_c]
    cube_max = [cube[c]["max"] for c in cube_c]

    ax.fill_between(cube_c, cube_avgs, cube_max, alpha=0.15, color=CUBE_COLOR, label='avg–max')
    ax.plot(cube_c, cube_avgs, 'o-', color=CUBE_COLOR, linewidth=2.5,
            markersize=8, label='avg', markerfacecolor='white', markeredgewidth=2)
    ax.plot(cube_c, cube_p95, 's--', color=CUBE_COLOR, linewidth=2,
            markersize=7, label='p95', markerfacecolor='white', markeredgewidth=2)

    for c, v in zip(cube_c, cube_avgs):
        ax.annotate(fmt_ms(v), (c, v), textcoords="offset points",
                    xytext=(0, 12), ha='center', fontsize=9, fontweight='bold', color=CUBE_COLOR)

    ax.set_xlabel('Concurrency', fontsize=12)
    ax.set_ylabel('Latency (ms)', fontsize=12)
    ax.set_title('CubeSandbox', fontsize=14, fontweight='bold', color=CUBE_COLOR)
    ax.set_xticks(cube_c)
    ax.legend(fontsize=9, loc='upper left')
    ax.grid(alpha=0.3)

    fig.suptitle('Latency Distribution by Concurrency Level',
                 fontsize=16, fontweight='bold', y=1.02)
    fig.tight_layout()
    fig.savefig(f'{OUTPUT_DIR}/chart_percentile_lines.png', dpi=150, bbox_inches='tight')
    plt.close(fig)
    print("  Saved chart_percentile_lines.png")


# ── Chart 3: Scaling Behavior (Overlay) ────────────────────────────────

def plot_scaling_overlay():
    fig, ax = plt.subplots(figsize=(10, 6.5))

    e2b_c = [1, 2, 5, 10]
    e2b_avgs = [e2b[c]["avg"] for c in e2b_c]
    e2b_p95 = [e2b[c]["p95"] for c in e2b_c]

    cube_c = [1, 5, 10]
    cube_avgs = [cube[c]["avg"] for c in cube_c]
    cube_p95 = [cube[c]["p95"] for c in cube_c]

    # E2B
    ax.fill_between(e2b_c, e2b_avgs, e2b_p95, alpha=0.15, color=E2B_COLOR)
    ax.plot(e2b_c, e2b_avgs, 'o-', color=E2B_COLOR, linewidth=2.5,
            markersize=9, label='E2B avg', markerfacecolor='white', markeredgewidth=2.5)
    ax.plot(e2b_c, e2b_p95, 'o--', color=E2B_LIGHT, linewidth=1.5,
            markersize=7, label='E2B p95', markerfacecolor='white', markeredgewidth=2)

    # CubeSandbox
    ax.fill_between(cube_c, cube_avgs, cube_p95, alpha=0.15, color=CUBE_COLOR)
    ax.plot(cube_c, cube_avgs, 's-', color=CUBE_COLOR, linewidth=2.5,
            markersize=9, label='CubeSandbox avg', markerfacecolor='white', markeredgewidth=2.5)
    ax.plot(cube_c, cube_p95, 's--', color=CUBE_LIGHT, linewidth=1.5,
            markersize=7, label='CubeSandbox p95', markerfacecolor='white', markeredgewidth=2)

    # Value labels for avg
    for c, v in zip(e2b_c, e2b_avgs):
        ax.annotate(fmt_ms(v), (c, v), textcoords="offset points",
                    xytext=(0, -18), ha='center', fontsize=10, fontweight='bold', color=E2B_COLOR)
    for c, v in zip(cube_c, cube_avgs):
        ax.annotate(fmt_ms(v), (c, v), textcoords="offset points",
                    xytext=(0, 12), ha='center', fontsize=10, fontweight='bold', color=CUBE_COLOR)

    # Dummy orchestrator reference line
    ax.axhline(y=e2b_dummy["avg"], color=DUMMY_COLOR, linestyle=':', linewidth=1.5, alpha=0.7)
    ax.annotate(f'E2B Dummy Orchestrator\n({e2b_dummy["avg"]}ms — unfair mock)',
                xy=(6, e2b_dummy["avg"]), xytext=(6, 120),
                fontsize=9, color=DUMMY_COLOR, style='italic',
                arrowprops=dict(arrowstyle='->', color=DUMMY_COLOR, lw=1.2))

    ax.set_xlabel('Concurrency', fontsize=13)
    ax.set_ylabel('Latency (ms)', fontsize=13)
    ax.set_title('Scaling Behavior: E2B (Real FC) vs CubeSandbox',
                 fontsize=15, fontweight='bold', pad=15)
    ax.set_xticks([1, 2, 5, 10])
    ax.legend(fontsize=10, loc='upper left', framealpha=0.9)
    ax.grid(alpha=0.3)
    ax.set_ylim(0, 1000)

    fig.tight_layout()
    fig.savefig(f'{OUTPUT_DIR}/chart_scaling_overlay.png', dpi=150, bbox_inches='tight')
    plt.close(fig)
    print("  Saved chart_scaling_overlay.png")


# ── Chart 4: Fairness Comparison (Before vs After) ─────────────────────

def plot_fairness_comparison():
    fig, ax = plt.subplots(figsize=(10, 6))

    categories = ['E2B Dummy\n(unfair)', 'E2B Real FC\n(fair)', 'CubeSandbox']
    values = [28, 315, 258]
    colors = [DUMMY_COLOR, E2B_COLOR, CUBE_COLOR]
    edge_colors = ['#757575', '#BF360C', '#1565C0']

    bars = ax.bar(categories, values, color=colors, edgecolor=edge_colors,
                  linewidth=1.5, width=0.5, zorder=3)

    # Value labels
    for bar, val in zip(bars, values):
        ax.text(bar.get_x() + bar.get_width()/2, bar.get_height() + 8,
                fmt_ms(val), ha='center', va='bottom', fontsize=14,
                fontweight='bold')

    # Annotations
    ax.annotate('Only HTTP + gRPC\n+ map write\n(No real VM)', (0, 28),
                xytext=(0, -80), textcoords='offset points',
                ha='center', fontsize=9, color=DUMMY_COLOR,
                arrowprops=dict(arrowstyle='->', color=DUMMY_COLOR))
    ax.annotate('Full Firecracker VM\nNBD + network + UFFD\ncgroup + snapshot', (1, 315),
                xytext=(0, 50), textcoords='offset points',
                ha='center', fontsize=9, color='#BF360C',
                arrowprops=dict(arrowstyle='->', color='#BF360C'))
    ax.annotate('Full VM creation\nCloud Hypervisor', (2, 258),
                xytext=(0, 50), textcoords='offset points',
                ha='center', fontsize=9, color='#1565C0',
                arrowprops=dict(arrowstyle='->', color='#1565C0'))

    ax.set_ylabel('Single-Concurrency Avg Latency (ms)', fontsize=12)
    ax.set_title('Why the Old E2B Benchmark Was Unfair',
                 fontsize=15, fontweight='bold', pad=15)
    ax.grid(axis='y', alpha=0.3, zorder=0)
    ax.set_axisbelow(True)
    ax.set_ylim(0, 450)

    # Add a "fair zone" indicator
    ax.axhspan(200, 400, alpha=0.08, color='green', zorder=0)
    ax.text(2.3, 300, '← Fair\n   comparison\n   zone', fontsize=10,
            color='#2E7D32', style='italic', va='center')

    fig.tight_layout()
    fig.savefig(f'{OUTPUT_DIR}/chart_fairness_comparison.png', dpi=150, bbox_inches='tight')
    plt.close(fig)
    print("  Saved chart_fairness_comparison.png")


# ── Main ──────────────────────────────────────────────────────────────

if __name__ == "__main__":
    print("Generating charts...")
    plot_avg_latency_bars()
    plot_percentile_lines()
    plot_scaling_overlay()
    plot_fairness_comparison()
    print(f"Done! Charts saved to {OUTPUT_DIR}/")
