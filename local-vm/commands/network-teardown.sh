#!/usr/bin/env bash
# Tear down bridge networking for E2B VMs
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/../lib/common.sh"

require_root

info "Tearing down E2B bridge network..."

# Stop dnsmasq
if [[ -f "$DNSMASQ_PID" ]]; then
  PID=$(cat "$DNSMASQ_PID" 2>/dev/null || true)
  if [[ -n "$PID" ]] && kill -0 "$PID" 2>/dev/null; then
    debug "Stopping dnsmasq (PID: $PID)..."
    kill "$PID"
    sleep 1
  fi
  rm -f "$DNSMASQ_PID"
fi
rm -f "$DNSMASQ_CONF"

# Remove iptables rules
DEFAULT_IFACE=$(ip route | grep '^default' | head -1 | awk '{print $5}')
if [[ -n "$DEFAULT_IFACE" ]]; then
  iptables -t nat -D POSTROUTING -s "$BRIDGE_CIDR" -o "$DEFAULT_IFACE" -j MASQUERADE 2>/dev/null || true
  iptables -D FORWARD -i "$BRIDGE" -o "$DEFAULT_IFACE" -j ACCEPT 2>/dev/null || true
  iptables -D FORWARD -i "$DEFAULT_IFACE" -o "$BRIDGE" -m state --state RELATED,ESTABLISHED -j ACCEPT 2>/dev/null || true
  iptables -D FORWARD -i "$BRIDGE" -o "$BRIDGE" -j ACCEPT 2>/dev/null || true
  iptables -D INPUT -i "$BRIDGE" -p udp --dport 67 -j ACCEPT 2>/dev/null || true
  iptables -D INPUT -i "$BRIDGE" -p udp --dport 68 -j ACCEPT 2>/dev/null || true
  iptables -D INPUT -i "$BRIDGE" -s "$BRIDGE_CIDR" -j ACCEPT 2>/dev/null || true
fi

# Remove TAP devices
RUNNING_TAPS=$(ip link show 2>/dev/null | grep -o 'e2b-tap-[^ :@]*' || true)
for TAP in $RUNNING_TAPS; do
  debug "Removing TAP device: $TAP..."
  ip link set "$TAP" down 2>/dev/null || true
  ip tuntap del "$TAP" mode tap 2>/dev/null || true
done

# Remove bridge
if ip link show "$BRIDGE" &>/dev/null; then
  debug "Removing bridge $BRIDGE..."
  ip link set "$BRIDGE" down
  ip link del "$BRIDGE"
fi

info "E2B bridge network teardown complete."
