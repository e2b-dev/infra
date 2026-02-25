#!/usr/bin/env bash
set -euo pipefail

###############################################################################
# e2b-build.sh
#
# Builds a qcow2 VM image with E2B infrastructure.
# Assembles cloud-init from vm/ templates + lib/env.sh, then runs two-phase
# QEMU build (Phase 1: packages + HWE kernel, Phase 2: make targets + template).
#
# Usage:
#   sudo ./e2b-build.sh [OPTIONS]
#
# Options:
#   --disk-size SIZE    Disk size for the qcow2 image (default: 60G)
#   --ram RAM           RAM for the build VM in MB (default: 16384)
#   --cpus CPUS         vCPUs for the build VM (default: 6)
#   --output FILE       Output qcow2 path (default: ./images/e2b-infra-amd64.qcow2)
#   --ssh-port PORT     Host port forwarded to VM SSH (default: 2222)
#   --commit HASH       Git commit/tag/branch to checkout (default: main)
#   --skip-build        Skip VM build, just generate cloud-init
#   --help              Show this help
###############################################################################

# ── Script directory (all paths relative to this) ────────────────────────────
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/lib/common.sh"

# ── Defaults ─────────────────────────────────────────────────────────────────
DISK_SIZE="60G"
RAM="16384"
CPUS="6"
OUTPUT="${SCRIPT_DIR}/images/e2b-infra-amd64.qcow2"
SSH_PORT="2222"
COMMIT_HASH=""
SKIP_BUILD=false
WORK_DIR="${SCRIPT_DIR}/.e2b-vm-build"
UBUNTU_IMG_URL="https://cloud-images.ubuntu.com/jammy/current/jammy-server-cloudimg-amd64.img"
LOG_FILE="${SCRIPT_DIR}/logs/build-e2b-vm.log"

# ── Parse arguments ──────────────────────────────────────────────────────────
while [[ $# -gt 0 ]]; do
  case $1 in
    --disk-size) DISK_SIZE="$2"; shift 2 ;;
    --ram)       RAM="$2";       shift 2 ;;
    --cpus)      CPUS="$2";      shift 2 ;;
    --output)    OUTPUT="$2";    shift 2 ;;
    --ssh-port)  SSH_PORT="$2";  shift 2 ;;
    --commit)    COMMIT_HASH="$2"; shift 2 ;;
    --skip-build) SKIP_BUILD=true; shift ;;
    --help)
      sed -n '/^# Usage:/,/^###/p' "$0" | head -n -1
      exit 0
      ;;
    *) error "Unknown option: $1"; exit 1 ;;
  esac
done

# ── Logging ──────────────────────────────────────────────────────────────────
mkdir -p "${SCRIPT_DIR}/logs" "${SCRIPT_DIR}/images"
exec &> >(tee -a "$LOG_FILE")

info "E2B Infrastructure VM Build"
info "Started: $(date)"

# ── Pre-flight checks ───────────────────────────────────────────────────────
require_root

info "Checking host prerequisites..."

install_if_missing() {
  local cmd="$1" pkg="${2:-$1}"
  if ! command -v "$cmd" &>/dev/null; then
    info "Installing $pkg..."
    apt-get update -qq && apt-get install -y -qq "$pkg"
  fi
}

install_if_missing qemu-system-x86_64 qemu-system-x86
install_if_missing qemu-img qemu-utils
install_if_missing genisoimage genisoimage
install_if_missing wget wget
install_if_missing ssh openssh-client
install_if_missing socat socat

# Check KVM
if [[ ! -e /dev/kvm ]]; then
  warn "/dev/kvm not found. Build will be very slow without KVM."
  KVM_FLAG=""
  MACHINE_FLAG="-machine q35,accel=tcg"
  CPU_FLAG="-cpu max"
else
  KVM_FLAG="-enable-kvm"
  MACHINE_FLAG="-machine q35,accel=kvm"
  CPU_FLAG="-cpu host"
fi

# ── Generate SSH key pair if needed ──────────────────────────────────────────
SSH_KEY_DIR="$HOME/.ssh"
SSH_KEY_FILE="${SSH_KEY_DIR}/e2b_vm_key"
mkdir -p "$SSH_KEY_DIR"
if [[ ! -f "$SSH_KEY_FILE" ]]; then
  info "Generating SSH key pair for VM access..."
  ssh-keygen -t ed25519 -f "$SSH_KEY_FILE" -N "" -C "e2b-vm-access"
fi
SSH_PUBKEY=$(cat "${SSH_KEY_FILE}.pub")
debug "SSH key: $SSH_KEY_FILE"

# ── Resolve OUTPUT to absolute path before changing directories ───────────────
mkdir -p "$(dirname "$OUTPUT")"
OUTPUT="$(cd "$(dirname "$OUTPUT")" && pwd)/$(basename "$OUTPUT")"

# ── Prepare working directory ────────────────────────────────────────────────
mkdir -p "$WORK_DIR"
cd "$WORK_DIR"

# ── Download Ubuntu 22.04 cloud image ───────────────────────────────────────
if [[ ! -f jammy-server-cloudimg-amd64.img ]]; then
  info "Downloading Ubuntu 22.04 cloud image..."
  wget -q --show-progress -O jammy-server-cloudimg-amd64.img "$UBUNTU_IMG_URL"
fi

# ── Create the qcow2 disk (skip if --skip-build) ────────────────────────────
if [[ "$SKIP_BUILD" != true ]]; then
  info "Creating qcow2 disk ($DISK_SIZE)..."
  cp jammy-server-cloudimg-amd64.img "$OUTPUT"
  qemu-img resize "$OUTPUT" "$DISK_SIZE"
fi

# ══════════════════════════════════════════════════════════════════════════════
# CLOUD-INIT ASSEMBLY
# ══════════════════════════════════════════════════════════════════════════════
info "Assembling cloud-init configuration..."

cat > meta-data <<EOF
instance-id: e2b-infra-vm
local-hostname: e2b-infra
EOF

cat > network-config <<EOF
version: 2
ethernets:
  id0:
    match:
      name: "en*"
    dhcp4: true
    dhcp6: false
  id1:
    match:
      name: "eth*"
    dhcp4: true
    dhcp6: false
EOF

# emit_write_file: outputs a cloud-init write_files entry for a local file
# Args: VM_PATH PERMISSIONS SOURCE_FILE
emit_write_file() {
  local vm_path="$1" perms="$2" src="$3"
  printf '  - path: %s\n' "$vm_path"
  printf '    permissions: "%s"\n' "$perms"
  printf '    content: |\n'
  sed 's/^/      /' "$src"
  printf '\n'
}

# Build the VM scripts block into a temp file (avoids awk -v escape mangling)
SCRIPTS_BLOCK=$(mktemp)
emit_write_file /opt/e2b/env.sh           "0755" "${SCRIPT_DIR}/lib/env.sh"       >> "$SCRIPTS_BLOCK"
emit_write_file /opt/e2b/deploy-phase1.sh "0755" "${SCRIPT_DIR}/vm/deploy-phase1.sh" >> "$SCRIPTS_BLOCK"
emit_write_file /opt/e2b/deploy-phase2.sh "0755" "${SCRIPT_DIR}/vm/deploy-phase2.sh" >> "$SCRIPTS_BLOCK"
emit_write_file /opt/e2b/start-all.sh     "0755" "${SCRIPT_DIR}/vm/start-all.sh"  >> "$SCRIPTS_BLOCK"
emit_write_file /opt/e2b/stop-all.sh      "0755" "${SCRIPT_DIR}/vm/stop-all.sh"   >> "$SCRIPTS_BLOCK"

# Read the cloud-init template and replace the __VM_SCRIPTS__ marker
TEMPLATE="${SCRIPT_DIR}/vm/cloud-init.yaml"
sed "/# __VM_SCRIPTS__/{
  r $SCRIPTS_BLOCK
  d
}" "$TEMPLATE" > user-data
rm -f "$SCRIPTS_BLOCK"

# Substitute placeholders
sed -i "s|__SSH_PUBKEY__|${SSH_PUBKEY}|g" user-data
sed -i "s|__COMMIT_HASH__|${COMMIT_HASH}|g" user-data

# ── Create cloud-init seed ISO ───────────────────────────────────────────────
info "Creating cloud-init seed ISO..."
genisoimage -output seed.iso -volid cidata -joliet -rock \
  user-data meta-data network-config 2>/dev/null

if [[ "$SKIP_BUILD" == true ]]; then
  info "--skip-build specified, skipping VM build phase."
  info "Cloud-init user-data written to: ${WORK_DIR}/user-data"
else
  # ══════════════════════════════════════════════════════════════════════════
  # PHASE 1: Boot VM with cloud-init — installs everything + HWE kernel
  # ══════════════════════════════════════════════════════════════════════════
  echo ""
  info "══════════════════════════════════════════════════════════════"
  info "  PHASE 1: Installing prerequisites + HWE kernel"
  info "══════════════════════════════════════════════════════════════"
  info "  Image:  $OUTPUT"
  info "  RAM:    ${RAM}MB | CPUs: $CPUS"
  if [[ -n "$COMMIT_HASH" ]]; then
    info "  Commit: $COMMIT_HASH"
  fi
  info ""
  info "  Monitor: ssh -i $SSH_KEY_FILE -p $SSH_PORT e2b@localhost"
  info "    Then: tail -f /var/log/e2b-phase1.log"
  info ""
  info "  The VM will shut itself down when Phase 1 completes."
  echo ""

  # Launch QEMU for Phase 1 (blocks until VM shuts down)
  qemu-system-x86_64 \
    ${KVM_FLAG} \
    ${CPU_FLAG} \
    ${MACHINE_FLAG} \
    -smp "$CPUS" \
    -m "$RAM" \
    -drive file="$OUTPUT",format=qcow2,if=virtio,cache=writeback \
    -drive file=seed.iso,format=raw,if=virtio,media=cdrom \
    -netdev user,id=net0,hostfwd=tcp::"$SSH_PORT"-:22 \
    -device virtio-net-pci,netdev=net0 \
    -nographic \
    -serial mon:stdio \
    -no-reboot

  echo ""
  info "Phase 1 complete. VM has shut down."

  # ══════════════════════════════════════════════════════════════════════════
  # PHASE 2: Boot into HWE kernel — run official make targets
  # ══════════════════════════════════════════════════════════════════════════
  echo ""
  info "══════════════════════════════════════════════════════════════"
  info "  PHASE 2: Booting into HWE kernel for official make targets"
  info "══════════════════════════════════════════════════════════════"
  info "  Runs official make targets on kernel 6.8+ for template support."
  info ""
  info "  Monitor: ssh -i $SSH_KEY_FILE -p $SSH_PORT e2b@localhost"
  info "    Then: tail -f /var/log/e2b-phase2.log"
  echo ""

  # Boot QEMU in background for Phase 2 (no cloud-init ISO)
  PHASE2_PID_FILE="/tmp/e2b-build-phase2.pid"
  PHASE2_MON_SOCK="/tmp/e2b-build-phase2-monitor.sock"
  rm -f "$PHASE2_PID_FILE" "$PHASE2_MON_SOCK"

  qemu-system-x86_64 \
    ${KVM_FLAG} \
    ${CPU_FLAG} \
    ${MACHINE_FLAG} \
    -smp "$CPUS" \
    -m "$RAM" \
    -drive file="$OUTPUT",format=qcow2,if=virtio,cache=writeback \
    -netdev user,id=net0,hostfwd=tcp::"$SSH_PORT"-:22 \
    -device virtio-net-pci,netdev=net0 \
    -monitor unix:"$PHASE2_MON_SOCK",server,nowait \
    -daemonize \
    -display none \
    -pidfile "$PHASE2_PID_FILE"

  QEMU_PID=$(cat "$PHASE2_PID_FILE")
  info "VM started (PID: $QEMU_PID). Waiting for SSH..."

  # Wait for SSH to become available
  for i in $(seq 1 120); do
    if ssh $SSH_OPTS -i "$SSH_KEY_FILE" -p "$SSH_PORT" e2b@localhost "echo ssh_ready" &>/dev/null; then
      info "SSH is ready."
      break
    fi
    if ! kill -0 "$QEMU_PID" 2>/dev/null; then
      error "VM exited before SSH became available."
      exit 1
    fi
    if (( i % 10 == 0 )); then
      debug "Still waiting for SSH... (${i}s)"
    fi
    sleep 1
  done

  if ! ssh $SSH_OPTS -i "$SSH_KEY_FILE" -p "$SSH_PORT" e2b@localhost "echo ssh_ready" &>/dev/null; then
    error "SSH not available after 120 seconds."
    kill "$QEMU_PID" 2>/dev/null || true
    exit 1
  fi

  # Verify the kernel version
  REMOTE_KVER=$(ssh $SSH_OPTS -i "$SSH_KEY_FILE" -p "$SSH_PORT" e2b@localhost "uname -r" 2>/dev/null || echo "unknown")
  info "VM kernel: $REMOTE_KVER"

  # Monitor Phase 2 progress (tail the log in background)
  echo ""
  echo "    ── Phase 2 build log ──────────────────────────────────"
  ssh $SSH_OPTS -i "$SSH_KEY_FILE" -p "$SSH_PORT" e2b@localhost \
    "sudo tail -f /var/log/e2b-phase2.log 2>/dev/null" &
  TAIL_PID=$!

  # Wait for QEMU process to exit (phase2.service shuts down VM when done)
  while kill -0 "$QEMU_PID" 2>/dev/null; do
    sleep 5
  done

  # Stop the tail process
  kill "$TAIL_PID" 2>/dev/null || true
  wait "$TAIL_PID" 2>/dev/null || true
  rm -f "$PHASE2_PID_FILE" "$PHASE2_MON_SOCK"

  echo ""
  echo "    ── End of Phase 2 build log ───────────────────────────"
  echo ""
  info "Phase 2 complete. VM has shut down."
fi

# ── Print summary ────────────────────────────────────────────────────────────
echo ""
info "════════════════════════════════════════════════════════════════"
info "  E2B Infrastructure VM Image Build Complete"
info "════════════════════════════════════════════════════════════════"
info ""
info "  Output image:    $OUTPUT"
if [[ -f "$OUTPUT" ]]; then
  info "  Image size:      $(du -h "$OUTPUT" | cut -f1)"
fi
info "  Disk capacity:   $DISK_SIZE"
if [[ -n "$COMMIT_HASH" ]]; then
  info "  Git commit:      $COMMIT_HASH"
fi
info ""
info "  Quick start (bridge networking, multi-instance):"
info "    sudo ./e2b-local.sh network setup    # one-time setup"
info "    sudo ./e2b-local.sh start            # start default instance"
info "    sudo ./e2b-local.sh start --name second"
info ""
info "  Quick start (port-forward, single instance):"
info "    sudo ./e2b-local.sh start --port-forward"
info ""
info "  SSH access:"
info "    ./e2b-local.sh ssh                   # default instance"
info "    ./e2b-local.sh ssh --name second     # named instance"
info ""
info "  All E2B services start automatically on boot via systemd."
info "════════════════════════════════════════════════════════════════"
info ""
info "Build finished: $(date)"
