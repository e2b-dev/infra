#!/usr/bin/env bash
# Set up bridge networking for E2B VMs
# Creates a bridge (e2b-br0), configures dnsmasq for DHCP, and sets up NAT.
# Idempotent — safe to run multiple times.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/../lib/common.sh"

require_root
init_bridge_networking

# Install dnsmasq if missing
if ! command -v dnsmasq &>/dev/null; then
  info "Installing dnsmasq..."
  apt-get update -qq && apt-get install -y -qq dnsmasq
  systemctl stop dnsmasq 2>/dev/null || true
  systemctl disable dnsmasq 2>/dev/null || true
fi

# Create bridge if it doesn't exist
if ! ip link show "$BRIDGE" &>/dev/null; then
  info "Creating bridge $BRIDGE..."
  ip link add name "$BRIDGE" type bridge
  ip addr add "${BRIDGE_IP}/24" dev "$BRIDGE"
  ip link set "$BRIDGE" up
  debug "Bridge $BRIDGE created with IP $BRIDGE_IP/24"
else
  debug "Bridge $BRIDGE already exists."
  if ! ip addr show "$BRIDGE" | grep -q "$BRIDGE_IP"; then
    ip addr add "${BRIDGE_IP}/24" dev "$BRIDGE" 2>/dev/null || true
  fi
  ip link set "$BRIDGE" up
fi

# Configure dnsmasq
debug "Configuring dnsmasq for DHCP on $BRIDGE..."
mkdir -p /etc/dnsmasq.d /var/lib/misc

cat > "$DNSMASQ_CONF" <<EOF
interface=$BRIDGE
bind-interfaces
except-interface=lo
dhcp-range=${DHCP_RANGE_START},${DHCP_RANGE_END},${BRIDGE_MASK},${DHCP_LEASE_TIME}
dhcp-option=option:router,${BRIDGE_IP}
dhcp-option=option:dns-server,8.8.8.8,8.8.4.4
dhcp-authoritative
log-dhcp
dhcp-leasefile=${DNSMASQ_LEASE}
pid-file=${DNSMASQ_PID}
port=0
EOF

if [[ -f "$DNSMASQ_PID" ]]; then
  OLD_PID=$(cat "$DNSMASQ_PID" 2>/dev/null || true)
  if [[ -n "$OLD_PID" ]] && kill -0 "$OLD_PID" 2>/dev/null; then
    debug "Stopping existing dnsmasq (PID: $OLD_PID)..."
    kill "$OLD_PID" 2>/dev/null || true
    sleep 1
  fi
  rm -f "$DNSMASQ_PID"
fi

debug "Starting dnsmasq..."
touch "$DNSMASQ_LEASE"
dnsmasq --conf-file="$DNSMASQ_CONF"
debug "dnsmasq started (PID: $(cat "$DNSMASQ_PID"))"

# Enable IP forwarding (required for NAT — defaults to 0 on clean hosts)
if [[ "$(sysctl -n net.ipv4.ip_forward)" != "1" ]]; then
  debug "Enabling net.ipv4.ip_forward..."
  sysctl -w net.ipv4.ip_forward=1 >/dev/null
fi

# iptables: MASQUERADE for outbound NAT
DEFAULT_IFACE=$(ip route | grep '^default' | head -1 | awk '{print $5}')
if [[ -z "$DEFAULT_IFACE" ]]; then
  warn "Could not determine default network interface for NAT."
else
  if ! iptables -t nat -C POSTROUTING -s "$BRIDGE_CIDR" -o "$DEFAULT_IFACE" -j MASQUERADE 2>/dev/null; then
    debug "Adding NAT MASQUERADE rule..."
    iptables -t nat -A POSTROUTING -s "$BRIDGE_CIDR" -o "$DEFAULT_IFACE" -j MASQUERADE
  fi
fi

# iptables: FORWARD rules
if [[ -n "$DEFAULT_IFACE" ]]; then
  if ! iptables -C FORWARD -i "$BRIDGE" -o "$DEFAULT_IFACE" -j ACCEPT 2>/dev/null; then
    iptables -I FORWARD -i "$BRIDGE" -o "$DEFAULT_IFACE" -j ACCEPT
  fi
  if ! iptables -C FORWARD -i "$DEFAULT_IFACE" -o "$BRIDGE" -m state --state RELATED,ESTABLISHED -j ACCEPT 2>/dev/null; then
    iptables -I FORWARD -i "$DEFAULT_IFACE" -o "$BRIDGE" -m state --state RELATED,ESTABLISHED -j ACCEPT
  fi
  if ! iptables -C FORWARD -i "$BRIDGE" -o "$BRIDGE" -j ACCEPT 2>/dev/null; then
    iptables -I FORWARD -i "$BRIDGE" -o "$BRIDGE" -j ACCEPT
  fi
fi

# iptables: INPUT rules (needed when bridge-nf-call-iptables=1 + INPUT DROP)
if ! iptables -C INPUT -i "$BRIDGE" -p udp --dport 67 -j ACCEPT 2>/dev/null; then
  iptables -I INPUT -i "$BRIDGE" -p udp --dport 67 -j ACCEPT
fi
if ! iptables -C INPUT -i "$BRIDGE" -p udp --dport 68 -j ACCEPT 2>/dev/null; then
  iptables -I INPUT -i "$BRIDGE" -p udp --dport 68 -j ACCEPT
fi
if ! iptables -C INPUT -i "$BRIDGE" -s "$BRIDGE_CIDR" -j ACCEPT 2>/dev/null; then
  iptables -I INPUT -i "$BRIDGE" -s "$BRIDGE_CIDR" -j ACCEPT
fi

info "Bridge $BRIDGE ready (${BRIDGE_IP}/24, DHCP: ${DHCP_RANGE_START}-${DHCP_RANGE_END})"
