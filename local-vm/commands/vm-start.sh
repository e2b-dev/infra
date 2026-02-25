#!/usr/bin/env bash
# Start the E2B infrastructure VM
# Networking: bridge mode (default) or --port-forward for legacy single-instance.
#
# Usage: e2b-local.sh start [OPTIONS]
# Options:
#   --name NAME       Instance name (default: "default")
#   --disk FILE       Path to qcow2 image
#   --ram MB          RAM in MB (default: 16384)
#   --cpus N          vCPUs (default: 6)
#   --port-forward    Legacy port-forwarding mode
#   --ssh-port PORT   SSH port for port-forward mode (default: 2222)

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/../lib/common.sh"

DEFAULT_DISK="${SCRIPT_DIR}/../images/e2b-infra-amd64.qcow2"

INSTANCE_NAME="default"
DISK=""
RAM="16384"
CPUS="6"
PORT_FORWARD=false
SSH_PORT="2222"

while [[ $# -gt 0 ]]; do
  case $1 in
    --name)         INSTANCE_NAME="$2"; shift 2 ;;
    --disk)         DISK="$2"; shift 2 ;;
    --ram)          RAM="$2"; shift 2 ;;
    --cpus)         CPUS="$2"; shift 2 ;;
    --port-forward) PORT_FORWARD=true; shift ;;
    --ssh-port)     SSH_PORT="$2"; shift 2 ;;
    *) error "Unknown option: $1"; exit 1 ;;
  esac
done

[[ -z "$DISK" ]] && DISK="$DEFAULT_DISK"

if [[ ! "$INSTANCE_NAME" =~ ^[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?$ ]]; then
  error "Instance name must be alphanumeric (hyphens allowed, not at start/end)."
  exit 1
fi

STATE_DIR="/tmp/e2b-vm-${INSTANCE_NAME}"
PID_FILE="${STATE_DIR}/pid"
MONITOR_SOCK="${STATE_DIR}/monitor.sock"
TAP_FILE="${STATE_DIR}/tap"
MAC_FILE="${STATE_DIR}/mac"
IP_FILE="${STATE_DIR}/ip"

if [[ -f "$PID_FILE" ]]; then
  PID=$(cat "$PID_FILE")
  if kill -0 "$PID" 2>/dev/null; then
    error "E2B VM instance '$INSTANCE_NAME' is already running (PID: $PID)"
    info "Stop it first with: e2b-local.sh stop --name $INSTANCE_NAME"
    exit 1
  else
    warn "Stale state for instance '$INSTANCE_NAME', cleaning up..."
    if [[ -f "$TAP_FILE" ]]; then
      OLD_TAP=$(cat "$TAP_FILE")
      ip link set "$OLD_TAP" down 2>/dev/null || true
      ip tuntap del "$OLD_TAP" mode tap 2>/dev/null || true
    fi
    rm -rf "$STATE_DIR"
  fi
fi
mkdir -p "$STATE_DIR"

if [[ ! -f "$DISK" ]]; then
  error "Disk image not found: $DISK"
  exit 1
fi

# Check KVM
if [[ ! -e /dev/kvm ]]; then
  error "/dev/kvm not found. KVM is required to run VMs."
  exit 1
fi

# Generate deterministic MAC from instance name
MAC="52:54:00:e2:$(echo -n "$INSTANCE_NAME" | md5sum | sed 's/\(..\)\(..\).*/\1:\2/')"
echo "$MAC" > "$MAC_FILE"

# Print connection info (called after VM starts)
print_env_vars() {
  local ip="$1" ssh_cmd="$2"
  echo ""
  info "SSH:  $ssh_cmd"
  info "API:  http://${ip}:80"
  echo ""
  info "Environment variables for E2B SDK/CLI:"
  echo "  export E2B_API_KEY=\"e2b_00000000000000000000000000000000\""
  echo "  export E2B_API_URL=\"http://${ip}:80\""
  echo "  export E2B_SANDBOX_URL=\"http://${ip}:3002\""
  echo "  export E2B_ACCESS_TOKEN=\"sk_e2b_00000000000000000000000000000000\""
}

# ── PORT-FORWARD MODE ──────────────────────────────────────────────────
if [[ "$PORT_FORWARD" == true ]]; then
  info "Starting E2B VM '$INSTANCE_NAME' (port-forward mode)..."

  qemu-system-x86_64 \
    -enable-kvm -cpu host -machine q35,accel=kvm \
    -smp "$CPUS" -m "$RAM" \
    -drive file="$DISK",format=qcow2,if=virtio,cache=writeback \
    -netdev user,id=net0,hostfwd=tcp::"$SSH_PORT"-:22,hostfwd=tcp::80-:80,hostfwd=tcp::3002-:3002,hostfwd=tcp::3003-:3003,hostfwd=tcp::5008-:5008,hostfwd=tcp::53000-:53000,hostfwd=tcp::5432-:5432,hostfwd=tcp::6379-:6379,hostfwd=tcp::8123-:8123,hostfwd=tcp::3100-:3100,hostfwd=tcp::4317-:4317,hostfwd=tcp::4318-:4318 \
    -device virtio-net-pci,netdev=net0,mac="$MAC" \
    -monitor unix:"$MONITOR_SOCK",server,nowait \
    -daemonize -display none -pidfile "$PID_FILE"

  echo "localhost" > "$IP_FILE"
  echo "$SSH_PORT" > "${STATE_DIR}/ssh_port"
  info "VM started (PID: $(cat "$PID_FILE"))"
  print_env_vars "localhost" "ssh -p $SSH_PORT e2b@localhost"
  exit 0
fi

# ── BRIDGE MODE ────────────────────────────────────────────────────────
require_root

if ! ip link show "$BRIDGE" &>/dev/null; then
  error "Bridge $BRIDGE not found. Run: sudo e2b-local.sh network setup"
  exit 1
fi

TAP="e2b-tap-${INSTANCE_NAME}"
echo "$TAP" > "$TAP_FILE"

if ip link show "$TAP" &>/dev/null; then
  ip link set "$TAP" down 2>/dev/null || true
  ip tuntap del "$TAP" mode tap 2>/dev/null || true
fi

info "Starting E2B VM '$INSTANCE_NAME' (bridge mode)..."
debug "TAP: $TAP | MAC: $MAC | Bridge: $BRIDGE"
debug "Disk: $DISK | RAM: ${RAM}MB | CPUs: $CPUS"

ip tuntap add "$TAP" mode tap
ip link set "$TAP" master "$BRIDGE"
ip link set "$TAP" up

qemu-system-x86_64 \
  -enable-kvm -cpu host -machine q35,accel=kvm \
  -smp "$CPUS" -m "$RAM" \
  -drive file="$DISK",format=qcow2,if=virtio,cache=writeback \
  -netdev tap,id=net0,ifname="$TAP",script=no,downscript=no \
  -device virtio-net-pci,netdev=net0,mac="$MAC" \
  -monitor unix:"$MONITOR_SOCK",server,nowait \
  -daemonize -display none -pidfile "$PID_FILE"

info "VM started (PID: $(cat "$PID_FILE"))"

# Discover VM IP
info "Waiting for DHCP lease..."
VM_IP=""
for i in $(seq 1 60); do
  if [[ -f "$DNSMASQ_LEASE" ]]; then
    VM_IP=$(grep -i "${MAC}" "$DNSMASQ_LEASE" 2>/dev/null | awk '{print $3}' | tail -1 || true)
  fi
  if [[ -z "$VM_IP" ]]; then
    VM_IP=$(ip neigh show dev "$BRIDGE" 2>/dev/null | grep -i "${MAC}" | awk '{print $1}' | head -1 || true)
  fi
  if [[ -n "$VM_IP" ]]; then
    echo "$VM_IP" > "$IP_FILE"
    info "VM IP: $VM_IP"
    break
  fi
  if (( i % 10 == 0 )); then debug "Still waiting for DHCP... (${i}s)"; fi
  sleep 1
done

if [[ -z "$VM_IP" ]]; then
  warn "Could not obtain VM IP via DHCP after 60s. Check bridge networking."
else
  info "Waiting for SSH..."
  for i in $(seq 1 60); do
    if ssh $SSH_OPTS -i "$SSH_KEY" "e2b@${VM_IP}" "echo ssh_ready" &>/dev/null; then
      info "SSH is ready."
      break
    fi
    if (( i % 10 == 0 )); then debug "Still waiting for SSH... (${i}s)"; fi
    sleep 1
  done
fi

print_env_vars "${VM_IP:-<pending>}" "ssh -i ~/.ssh/e2b_vm_key e2b@${VM_IP:-<pending>}"
