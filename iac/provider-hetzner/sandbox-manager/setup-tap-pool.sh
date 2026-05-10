#!/usr/bin/env bash
# Pre-create N TAP devices for warm-pool of persistent FC VMs
set -e
NUM_TAPS=${MAXICORE_TAP_POOL_SIZE:-10}

for i in $(seq 0 $((NUM_TAPS-1))); do
    TAP="mxtap$i"
    BASE=$((i*4))
    HOST_IP="172.16.$BASE.1"

    ip link delete $TAP 2>/dev/null || true
    ip tuntap add dev $TAP mode tap user root
    ip addr add $HOST_IP/30 dev $TAP
    ip link set $TAP up
done

# IP forwarding + NAT
sysctl -w net.ipv4.ip_forward=1 >/dev/null
DEFAULT_IFACE=$(ip route show default | awk '{print $5}' | head -1)
iptables -t nat -C POSTROUTING -s 172.16.0.0/16 -o $DEFAULT_IFACE -j MASQUERADE 2>/dev/null || \
    iptables -t nat -A POSTROUTING -s 172.16.0.0/16 -o $DEFAULT_IFACE -j MASQUERADE
for i in $(seq 0 $((NUM_TAPS-1))); do
    iptables -C FORWARD -i mxtap$i -j ACCEPT 2>/dev/null || iptables -A FORWARD -i mxtap$i -j ACCEPT
    iptables -C FORWARD -o mxtap$i -j ACCEPT 2>/dev/null || iptables -A FORWARD -o mxtap$i -j ACCEPT
done
echo "✅ TAP-pool of $NUM_TAPS devices ready"
