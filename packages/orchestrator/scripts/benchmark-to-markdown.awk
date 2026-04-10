#!/usr/bin/awk -f
# benchmark-to-markdown.awk - Convert Go benchmark results to a Markdown table.
#
# The output of benchmark needs to be grepped first
# grep 'BenchmarkConcurrentResume/concurrency-[0-9]\+-' bench.log | column -t > results.log
#
# Usage:
#   awk -f benchmark-to-markdown.awk results.log
#   cat results.log | awk -f benchmark-to-markdown.awk

BEGIN {
    printf "| %-11s | %-4s | %-8s | %-8s | %-8s | %-8s | %-8s | %-8s | %-14s | %-14s | %-4s |\n",
        "Concurrency", "Runs", "Avg (ms)", "Min (ms)", "Max (ms)",
        "P50 (ms)", "P95 (ms)", "P99 (ms)", "Wall-clock (ms)", "Throughput/s", "Fail"
    printf "| %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s |\n",
        "-----------", "----", "--------", "--------", "--------",
        "--------", "--------", "--------", "--------------", "--------------", "----"
}

{
    # Extract concurrency from benchmark name (e.g. "BenchmarkConcurrentResume/concurrency-5-8")
    split($1, parts, "-")
    concurrency = parts[length(parts) - 1]

    runs = $2

    # Walk fields: value precedes its label
    avg = ""; fail = ""; max = ""; min = ""
    p50 = ""; p95 = ""; p99 = ""; wall = ""

    for (i = 3; i <= NF; i++) {
        if ($(i) == "avg-ms")        avg  = $(i-1)
        else if ($(i) == "fail")     fail = $(i-1)
        else if ($(i) == "max-ms")   max  = $(i-1)
        else if ($(i) == "min-ms")   min  = $(i-1)
        else if ($(i) == "p50-ms")   p50  = $(i-1)
        else if ($(i) == "p95-ms")   p95  = $(i-1)
        else if ($(i) == "p99-ms")   p99  = $(i-1)
        else if ($(i) == "wall-clock-ms") wall = $(i-1)
    }

    # Compute throughput: concurrency / (wall-clock in seconds)
    throughput = ""
    if (wall + 0 > 0) {
        throughput = sprintf("%.1f", (concurrency + 0) / ((wall + 0) / 1000))
    }

    printf "| %-11s | %-4s | %8s | %8s | %8s | %8s | %8s | %8s | %14s | %14s | %-4s |\n",
        concurrency, runs, avg, min, max, p50, p95, p99, wall, throughput, fail
}
