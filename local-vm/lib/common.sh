#!/usr/bin/env bash
# lib/common.sh — shared constants and helpers for all e2b-lite-build scripts

# ── TUI: Colors (auto-disabled if not a TTY) ────────────────────────────────
if [[ -t 1 ]]; then
  C_RESET="\033[0m"  C_BOLD="\033[1m"
  C_GREEN="\033[32m"  C_RED="\033[31m"  C_YELLOW="\033[33m"  C_DIM="\033[2m"
else
  C_RESET="" C_BOLD="" C_GREEN="" C_RED="" C_YELLOW="" C_DIM=""
fi

E2B_VERBOSE="${E2B_VERBOSE:-0}"
E2B_QUIET="${E2B_QUIET:-0}"

# ── TUI: Output helpers ─────────────────────────────────────────────────────
info()  { [[ "$E2B_QUIET" == "1" ]] && return; printf "${C_GREEN}[INFO]${C_RESET} %s\n" "$*"; }
warn()  { printf "${C_YELLOW}[WARNING]${C_RESET} %s\n" "$*"; }
error() { printf "${C_RED}[ERROR]${C_RESET} %s\n" "$*" >&2; }
debug() { [[ "$E2B_VERBOSE" != "1" ]] && return; printf "${C_DIM}[DEBUG] %s${C_RESET}\n" "$*"; }
log()   { echo "[$(date '+%Y-%m-%d %H:%M:%S')] $*"; }

# ── Network: Dynamic bridge subnet detection ────────────────────────────────
_init_bridge_vars() {
  # If the bridge already exists, read its IP.
  if ip addr show "$BRIDGE" 2>/dev/null | grep -qoP 'inet 192\.168\.\d+\.1/24'; then
    local base
    base=$(ip addr show "$BRIDGE" 2>/dev/null | grep -oP 'inet \K192\.168\.\d+' | head -1)
    debug "Existing bridge at ${base}.0/24"
  elif [[ -n "${E2B_BRIDGE_SUBNET:-}" ]]; then
    local base="$E2B_BRIDGE_SUBNET"
    debug "Using E2B_BRIDGE_SUBNET override: ${base}"
  else
    # Auto-detect: try 192.168.100-119
    local base=""
    for i in $(seq 100 119); do
      local candidate="192.168.${i}"
      if ip route 2>/dev/null | grep -q "${candidate}.0/24"; then continue; fi
      if ip addr 2>/dev/null | grep -q "${candidate}.1"; then continue; fi
      base="$candidate"
      break
    done
    if [[ -z "$base" ]]; then
      error "No available subnet found in 192.168.100-119 range."
      exit 1
    fi
    debug "Auto-detected available subnet: ${base}.0/24"
  fi

  if [[ -z "$base" ]]; then
    error "Failed to detect bridge subnet."
    exit 1
  fi

  BRIDGE_IP="${base}.1"
  BRIDGE_CIDR="${base}.0/24"
  BRIDGE_MASK="255.255.255.0"
  DHCP_RANGE_START="${base}.10"
  DHCP_RANGE_END="${base}.254"
}

# ── Network constants ─────────────────────────────────────────────────────────
BRIDGE="e2b-br0"
DHCP_LEASE_TIME="12h"
DNSMASQ_CONF="/etc/dnsmasq.d/e2b-bridge.conf"
DNSMASQ_PID="/run/dnsmasq-e2b.pid"
DNSMASQ_LEASE="/var/lib/misc/dnsmasq-e2b.leases"
_init_bridge_vars

# ── SSH constants ─────────────────────────────────────────────────────────────
SSH_KEY="$HOME/.ssh/e2b_vm_key"
SSH_OPTS="-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o ConnectTimeout=5"

# ── Helpers ──────────────────────────────────────────────────────────────────
require_root() {
  if [[ $EUID -ne 0 ]]; then
    error "This script must be run as root."
    exit 1
  fi
}

e2b_vm_ip() {
  local ip_file="/tmp/e2b-vm-${1:-default}/ip"
  if [[ -f "$ip_file" ]]; then
    cat "$ip_file"
  fi
}
