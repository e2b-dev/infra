#!/usr/bin/env bash
set -u
STORAGE_DIR="${1:-/var/lib/e2b}"   # optional first arg override
OUT="/tmp/fc-leak-report-$(date +%Y%m%dT%H%M%S).txt"

echo "Firecracker leak quick-audit" > "$OUT"
echo "Timestamp: $(date -u +"%Y-%m-%dT%H:%M:%SZ")" >> "$OUT"
echo "Storage dir: $STORAGE_DIR" >> "$OUT"
echo "" >> "$OUT"

# helper
sep() { printf "\n==== %s ====" "$1" >> "$OUT"; printf "\n\n" >> "$OUT"; }

sep "Firecracker processes"
pgrep -af firecracker 2>/dev/null | tee -a "$OUT"
echo "" >> "$OUT"
echo "Count: $(pgrep -c firecracker 2>/dev/null || echo 0)" >> "$OUT"

sep "Firecracker process details (ps + state + /proc/<pid>/cmdline summary)"
if pids=$(pgrep -f firecracker 2>/dev/null); then
  for p in $pids; do
    printf "PID: %s\n" "$p" >> "$OUT"
    ps -o pid,ppid,stat,etime,cmd -p "$p" 2>/dev/null | sed -n '1,1p' >> "$OUT"
    ps -o pid,ppid,stat,etime,cmd -p "$p" 2>/dev/null | sed -n '2,2p' >> "$OUT"
    if [ -r "/proc/$p/fd" ]; then
      echo "Open files (first 20):" >> "$OUT"
      ls -l /proc/$p/fd 2>/dev/null | head -n 20 >> "$OUT"
    fi
    echo "" >> "$OUT"
  done
else
  echo "none" >> "$OUT"
fi

sep "Uninterruptible (D) state processes (possible stuck IO)"
ps -eo pid,ppid,stat,cmd | awk '$3 ~ /D/ {print}' | tee -a "$OUT" || true

sep "Network namespaces"
ip netns list 2>/dev/null | tee -a "$OUT" || echo "(ip netns not available or none)" >> "$OUT"
echo "" >> "$OUT"
echo "Netns count: $(ip netns list 2>/dev/null | wc -l || echo 0)" >> "$OUT"

sep "For each namespace: PIDs inside and top cmds"
for ns in $(ip netns list 2>/dev/null | awk '{print $1}' 2>/dev/null || true); do
  echo "Namespace: $ns" >> "$OUT"
  ip netns pids "$ns" 2>/dev/null | tee -a "$OUT" || echo "(no pids or no permission)" >> "$OUT"
  for pid in $(ip netns pids "$ns" 2>/dev/null || true); do
    ps -o pid,ppid,stat,etime,cmd -p "$pid" 2>/dev/null | sed -n '2p' >> "$OUT"
  done
  echo "" >> "$OUT"
done

sep "Network devices (veth/tap/vpeer) and counts"
ip link show 2>/dev/null | egrep -i 'veth|tap|vpeer' -n | tee -a "$OUT" || echo "(no veth/tap found)" >> "$OUT"
echo "" >> "$OUT"
echo "veth/tap count: $(ip link show 2>/dev/null | egrep -i 'veth|tap|vpeer' -c || echo 0)" >> "$OUT"

sep "iptables rules mentioning veth/tap"
if command -v iptables-save >/dev/null 2>&1; then
  iptables-save 2>/dev/null | egrep -i 'veth|tap|vpeer' -n | tee -a "$OUT" || echo "(no iptables lines matched)" >> "$OUT"
else
  echo "iptables-save not available" >> "$OUT"
fi

sep "Metrics FIFOs / sockets / uffd in storage"
find "$STORAGE_DIR" -xdev \( -type p -name '*metrics*' -o -name '*uffd*' -o -name '*fc*' -o -name '*sock*' \) -print 2>/dev/null | tee -a "$OUT" || echo "(no matches or permission denied)" >> "$OUT"
echo "" >> "$OUT"

sep "Unix domain sockets open by processes (ss -x)"
if command -v ss >/dev/null 2>&1; then
  ss -x -a -n -p 2>/dev/null | egrep -i 'firecracker|fc|nbd|uffd' -n | tee -a "$OUT" || echo "(no matching unix sockets)" >> "$OUT"
else
  echo "ss not installed" >> "$OUT"
fi

sep "Search for NBD-related files/sockets"
find "$STORAGE_DIR" -xdev \( -type s -o -name '*nbd*' -o -name '*nbd.sock*' -o -name '*nbd*' \) -print 2>/dev/null | tee -a "$OUT" || true

sep "NBD dispatch concurrent metric (if Prometheus available, not included here)"
echo "Check metric: orchestrator.nbd.dispatch.read.concurrent if Prometheus is configured." >> "$OUT"

sep "Orphan metrics FIFOs under /tmp and /var/run (common locations)"
find /tmp /var/run -maxdepth 3 -type p -name '*metrics*' -o -name '*fc*' -print 2>/dev/null | tee -a "$OUT" || true

sep "Summary counts"
echo "firecracker_count: $(pgrep -c firecracker 2>/dev/null || echo 0)" >> "$OUT"
echo "netns_count: $(ip netns list 2>/dev/null | wc -l || echo 0)" >> "$OUT"
echo "veth_tap_count: $(ip link show 2>/dev/null | egrep -i 'veth|tap|vpeer' -c || echo 0)" >> "$OUT"
echo "d_state_processes: $(ps -eo stat | egrep -c 'D' || echo 0)" >> "$OUT"

echo "" >> "$OUT"
echo "Tips:" >> "$OUT"
echo "- Run as root (sudo) for full visibility (netns, iptables, lsof info)." >> "$OUT"
echo "- If you find netns with PIDs inside, inspect those PIDs and their /proc/<pid>/fd to see held files." >> "$OUT"

echo ""
echo "Report generated: $OUT"
echo "Preview (first 200 lines):"
echo "----------------------------------------"
sed -n '1,200p' "$OUT"
