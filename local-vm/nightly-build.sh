#!/usr/bin/env bash
set -euo pipefail

###############################################################################
# nightly-build.sh
#
# Cron-friendly script that performs a full E2B VM build + test cycle.
# Produces a dated, verified qcow2 image with a "latest" symlink on success.
#
# Usage:
#   sudo ./nightly-build.sh [OPTIONS]
#
# Options:
#   --commit HASH     Git commit/tag/branch to build (default: main)
#   --skip-build      Skip build, just test an existing image
#   --keep-days N     Prune images older than N days (default: 7)
#   --image FILE      Test a specific image (with --skip-build)
#   --help            Show this help
#
# Cron example:
#   0 2 * * * /root/e2b-lite-build/local-vm/nightly-build.sh >> /root/e2b-lite-build/local-vm/logs/cron.log 2>&1
###############################################################################

# ── Script directory (all paths relative to this) ────────────────────────────
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/lib/common.sh"

# ── Defaults ─────────────────────────────────────────────────────────────────
DATE="$(date +%Y-%m-%d)"
COMMIT="main"
SKIP_BUILD=false
KEEP_DAYS=7
SPECIFIC_IMAGE=""
VM_NAME="dtest"
LOCKFILE="/tmp/e2b-nightly-build.lock"
LOG_FILE="${SCRIPT_DIR}/logs/nightly-build-${DATE}.log"
IMAGE_DIR="${SCRIPT_DIR}/images"
IMAGE_FILE="${IMAGE_DIR}/e2b-infra-${DATE}-amd64.qcow2"
LATEST_LINK="${IMAGE_DIR}/e2b-infra-latest-amd64.qcow2"
FAILED_DIR="${IMAGE_DIR}/failed"

# ── Parse arguments ──────────────────────────────────────────────────────────
while [[ $# -gt 0 ]]; do
  case $1 in
    --commit)     COMMIT="$2"; shift 2 ;;
    --skip-build) SKIP_BUILD=true; shift ;;
    --keep-days)  KEEP_DAYS="$2"; shift 2 ;;
    --image)      SPECIFIC_IMAGE="$2"; shift 2 ;;
    --help)
      sed -n '/^# Usage:/,/^###/p' "$0" | head -n -1
      exit 0
      ;;
    *) error "Unknown option: $1"; exit 1 ;;
  esac
done

# ── Logging ──────────────────────────────────────────────────────────────────
mkdir -p "${SCRIPT_DIR}/logs" "${IMAGE_DIR}"
exec &> >(tee -a "$LOG_FILE")

log "========================================"
log " E2B Nightly Build + Test"
log " Date:   ${DATE}"
log " Commit: ${COMMIT}"
log " Image:  ${IMAGE_FILE}"
log "========================================"

# ── Pre-flight checks ───────────────────────────────────────────────────────
require_root

# ── Lock file (prevent concurrent runs) ──────────────────────────────────────
if [[ -f "$LOCKFILE" ]]; then
  LOCK_PID=$(cat "$LOCKFILE" 2>/dev/null || true)
  if [[ -n "$LOCK_PID" ]] && kill -0 "$LOCK_PID" 2>/dev/null; then
    error "Another nightly build is running (PID: $LOCK_PID)."
    exit 1
  else
    warn "Stale lock file found, removing."
    rm -f "$LOCKFILE"
  fi
fi
echo $$ > "$LOCKFILE"
trap 'rm -f "$LOCKFILE"' EXIT

# ── Resolve image to test ────────────────────────────────────────────────────
if [[ -n "$SPECIFIC_IMAGE" ]]; then
  IMAGE_FILE="$SPECIFIC_IMAGE"
  log "Using specific image: ${IMAGE_FILE}"
fi

# ══════════════════════════════════════════════════════════════════════════════
# STEP 1: BUILD
# ══════════════════════════════════════════════════════════════════════════════
if [[ "$SKIP_BUILD" == true ]]; then
  log "Skipping build (--skip-build)."
  if [[ ! -f "$IMAGE_FILE" ]]; then
    # Try compressed version of dated image
    if [[ -f "${IMAGE_FILE}.xz" ]]; then
      log "Found compressed image, decompressing ${IMAGE_FILE}.xz..."
      xz -dk "${IMAGE_FILE}.xz"
    # Try latest symlink (points to .xz file)
    elif [[ -L "${LATEST_LINK}.xz" && -f "${LATEST_LINK}.xz" ]]; then
      COMPRESSED_FILE="$(readlink -f "${LATEST_LINK}.xz")"
      log "No dated image found, decompressing latest: ${COMPRESSED_FILE}"
      IMAGE_FILE="${COMPRESSED_FILE%.xz}"
      xz -dk "$COMPRESSED_FILE"
    else
      error "No image found at ${IMAGE_FILE} and no latest symlink at ${LATEST_LINK}.xz"
      exit 1
    fi
  fi
else
  log "Starting build..."
  BUILD_ARGS=(--output "$IMAGE_FILE" --commit "$COMMIT")

  if ! "${SCRIPT_DIR}/e2b-build.sh" "${BUILD_ARGS[@]}"; then
    error "Build failed!"
    exit 1
  fi

  if [[ ! -f "$IMAGE_FILE" ]]; then
    error "Build completed but image not found at ${IMAGE_FILE}."
    exit 1
  fi

  log "Build complete. Image: ${IMAGE_FILE} ($(du -h "$IMAGE_FILE" | cut -f1))"
fi

# ══════════════════════════════════════════════════════════════════════════════
# STEP 2: SETUP NETWORKING
# ══════════════════════════════════════════════════════════════════════════════
log "Setting up bridge networking..."
"${SCRIPT_DIR}/e2b-local.sh" network setup || {
  warn "Network setup had issues, continuing anyway..."
}

# ══════════════════════════════════════════════════════════════════════════════
# STEP 3: START VM
# ══════════════════════════════════════════════════════════════════════════════
log "Starting test VM (name: ${VM_NAME})..."

# Stop any existing test VM first
"${SCRIPT_DIR}/e2b-local.sh" stop --name "$VM_NAME" 2>/dev/null || true

"${SCRIPT_DIR}/e2b-local.sh" start --name "$VM_NAME" --disk "$IMAGE_FILE"

# Read the VM IP from state
VM_IP_FILE="/tmp/e2b-vm-${VM_NAME}/ip"
VM_IP=""
if [[ -f "$VM_IP_FILE" ]]; then
  VM_IP=$(cat "$VM_IP_FILE")
fi

if [[ -z "$VM_IP" ]]; then
  error "Could not determine VM IP."
  "${SCRIPT_DIR}/e2b-local.sh" stop --name "$VM_NAME" --force 2>/dev/null || true
  exit 1
fi

log "VM started at IP: ${VM_IP}"

# Verify SSH is actually reachable (vm-start.sh may not have confirmed it)
log "Verifying SSH connectivity..."
SSH_OPTS="$SSH_OPTS -o LogLevel=ERROR"
SSH_OK=false
for i in $(seq 1 90); do
  if ssh $SSH_OPTS -i "$SSH_KEY" "e2b@${VM_IP}" "echo ssh_ready" &>/dev/null; then
    SSH_OK=true
    log "SSH connectivity confirmed after ${i}s."
    break
  fi
  if (( i % 15 == 0 )); then
    log "  Still waiting for SSH... (${i}s)"
  fi
  sleep 1
done

if [[ "$SSH_OK" != true ]]; then
  error "SSH not reachable after 90s. VM may not have booted properly."
  "${SCRIPT_DIR}/e2b-local.sh" stop --name "$VM_NAME" --force 2>/dev/null || true
  exit 1
fi

# ══════════════════════════════════════════════════════════════════════════════
# STEP 4: WAIT FOR READINESS
# ══════════════════════════════════════════════════════════════════════════════
log "Waiting for API health check..."

API_READY=false
for i in $(seq 1 180); do
  if curl -sf --connect-timeout 3 --max-time 5 "http://${VM_IP}:80/health" &>/dev/null; then
    API_READY=true
    log "API health check passed after ${i}s."
    break
  fi
  if (( i % 15 == 0 )); then
    log "  Still waiting for API... (${i}s)"
  fi
  sleep 1
done

if [[ "$API_READY" != true ]]; then
  error "API health check failed after 180s."
  "${SCRIPT_DIR}/e2b-local.sh" stop --name "$VM_NAME" --force 2>/dev/null || true
  exit 1
fi

# Wait for orchestrator node registration
log "Waiting for orchestrator node registration..."

NODE_READY=false
for i in $(seq 1 90); do
  # Check API log for nodes_count > 0 (API output goes to /var/log/e2b-api.log)
  NODE_COUNT=$(ssh $SSH_OPTS -i "$SSH_KEY" "e2b@${VM_IP}" \
    "sudo grep -oP '\"nodes_count\":\\s*\\K[0-9]+' /var/log/e2b-api.log 2>/dev/null | tail -1" 2>/dev/null || echo "0")

  if [[ -z "$NODE_COUNT" ]]; then
    NODE_COUNT=0
  fi

  if (( NODE_COUNT > 0 )); then
    NODE_READY=true
    log "Orchestrator node registered (nodes_count: ${NODE_COUNT}) after ${i}s."
    break
  fi

  # Fallback: check e2b-infra.log and journalctl (in case log routing changes)
  if [[ "$NODE_READY" != true ]]; then
    FALLBACK_COUNT=$(ssh $SSH_OPTS -i "$SSH_KEY" "e2b@${VM_IP}" \
      "{ sudo grep -oP '\"nodes_count\":\\s*\\K[0-9]+' /var/log/e2b-infra.log 2>/dev/null; sudo journalctl -u e2b-infra --no-pager -n 100 2>/dev/null | grep -oP '\"nodes_count\":\\s*\\K[0-9]+'; } | tail -1" 2>/dev/null || echo "0")
    if [[ -n "$FALLBACK_COUNT" ]] && (( FALLBACK_COUNT > 0 )); then
      NODE_READY=true
      log "Orchestrator node registered (nodes_count: ${FALLBACK_COUNT}) after ${i}s."
      break
    fi
  fi

  if (( i % 15 == 0 )); then
    log "  Still waiting for node registration... (${i}s, nodes_count: ${NODE_COUNT})"
  fi
  sleep 1
done

if [[ "$NODE_READY" != true ]]; then
  warn "Node registration not confirmed after 90s. Proceeding with test anyway..."
  # Give it a bit more time
  sleep 10
fi

# ══════════════════════════════════════════════════════════════════════════════
# STEP 5: TEST
# ══════════════════════════════════════════════════════════════════════════════
log "Running sandbox test..."

# Install npm deps if needed
if [[ ! -d "${SCRIPT_DIR}/node_modules" ]]; then
  log "Installing npm dependencies..."
  (cd "$SCRIPT_DIR" && npm install --no-audit --no-fund 2>&1) || {
    error "npm install failed."
    "${SCRIPT_DIR}/e2b-local.sh" stop --name "$VM_NAME" --force 2>/dev/null || true
    exit 1
  }
fi

# Discover template ID from the VM
log "Discovering template ID..."
TEMPLATE_ID=""
# Method 1: Query the API
TEMPLATE_ID=$(curl -sf --connect-timeout 5 --max-time 10 \
  -H "X-API-Key: e2b_00000000000000000000000000000000" \
  "http://${VM_IP}:80/templates" 2>/dev/null | \
  python3 -c "import sys,json; ts=json.load(sys.stdin); print(ts[0].get('templateID',ts[0].get('envID','')))" 2>/dev/null || true)

if [[ -z "$TEMPLATE_ID" ]]; then
  # Method 2: Query PostgreSQL via SSH
  TEMPLATE_ID=$(ssh $SSH_OPTS -i "$SSH_KEY" "e2b@${VM_IP}" \
    "sudo docker exec \$(sudo docker ps -q -f name=postgres) psql -U postgres -t -c \"SELECT env_id FROM envs WHERE env_id != '' LIMIT 1;\"" 2>/dev/null | tr -d ' \n' || true)
fi

if [[ -n "$TEMPLATE_ID" ]]; then
  log "Using template: ${TEMPLATE_ID}"
else
  warn "Could not discover template ID, test-sandbox.mjs will try auto-discovery."
fi

TEST_PASSED=false
if (cd "$SCRIPT_DIR" && node test-sandbox.mjs "$VM_IP" ${TEMPLATE_ID:+"$TEMPLATE_ID"} 2>&1); then
  TEST_PASSED=true
  log "Sandbox test PASSED."
else
  log "Sandbox test FAILED."
fi

# ══════════════════════════════════════════════════════════════════════════════
# STEP 6: STOP VM (must happen before compression — xz on a live qcow2 corrupts it)
# ══════════════════════════════════════════════════════════════════════════════
log "Stopping test VM..."
# Gracefully stop services inside the VM before ACPI shutdown
if [[ -n "$VM_IP" ]]; then
  ssh $SSH_OPTS -i "$SSH_KEY" "e2b@${VM_IP}" \
    "sudo /opt/e2b/stop-all.sh 2>/dev/null; sudo sync; sudo sync" 2>/dev/null || true
  sleep 3
fi
"${SCRIPT_DIR}/e2b-local.sh" stop --name "$VM_NAME" 2>/dev/null || true

# ══════════════════════════════════════════════════════════════════════════════
# STEP 7: RESULT
# ══════════════════════════════════════════════════════════════════════════════
if [[ "$TEST_PASSED" == true ]]; then
  # Compress the verified image with xz (all cores), removes original
  COMPRESSED="${IMAGE_FILE}.xz"
  log "Compressing image with xz (-T0)..."
  xz -T0 -f "$IMAGE_FILE"
  log "  Compressed: ${COMPRESSED} ($(du -h "$COMPRESSED" | cut -f1))"

  log "Creating latest symlink..."
  ln -sf "$(basename "$COMPRESSED")" "${LATEST_LINK}.xz"
  log "SUCCESS: ${COMPRESSED}"
  log "  Latest: ${LATEST_LINK}.xz -> $(basename "$COMPRESSED")"
else
  log "FAIL: Moving image to failed/"
  mkdir -p "$FAILED_DIR"
  if [[ -f "$IMAGE_FILE" && "$SKIP_BUILD" != true ]]; then
    mv "$IMAGE_FILE" "${FAILED_DIR}/$(basename "$IMAGE_FILE")"
    log "  Moved to: ${FAILED_DIR}/$(basename "$IMAGE_FILE")"
  fi
fi

# Prune old images
if (( KEEP_DAYS > 0 )); then
  log "Pruning images older than ${KEEP_DAYS} days..."
  PRUNED=0
  for OLD_IMAGE in "${IMAGE_DIR}"/e2b-infra-????-??-??-amd64.qcow2.xz; do
    if [[ ! -f "$OLD_IMAGE" ]]; then continue; fi
    # Extract date from filename
    IMG_DATE=$(basename "$OLD_IMAGE" | grep -oP '\d{4}-\d{2}-\d{2}')
    if [[ -z "$IMG_DATE" ]]; then continue; fi
    # Compare with cutoff
    CUTOFF_DATE=$(date -d "-${KEEP_DAYS} days" +%Y-%m-%d 2>/dev/null || date -v-${KEEP_DAYS}d +%Y-%m-%d 2>/dev/null || true)
    if [[ -n "$CUTOFF_DATE" && "$IMG_DATE" < "$CUTOFF_DATE" ]]; then
      log "  Pruning old image: $(basename "$OLD_IMAGE")"
      rm -f "$OLD_IMAGE"
      PRUNED=$((PRUNED + 1))
    fi
  done
  # Also prune failed images
  for OLD_IMAGE in "${FAILED_DIR}"/e2b-infra-????-??-??-amd64.qcow2; do
    if [[ ! -f "$OLD_IMAGE" ]]; then continue; fi
    IMG_DATE=$(basename "$OLD_IMAGE" | grep -oP '\d{4}-\d{2}-\d{2}')
    if [[ -z "$IMG_DATE" ]]; then continue; fi
    CUTOFF_DATE=$(date -d "-${KEEP_DAYS} days" +%Y-%m-%d 2>/dev/null || date -v-${KEEP_DAYS}d +%Y-%m-%d 2>/dev/null || true)
    if [[ -n "$CUTOFF_DATE" && "$IMG_DATE" < "$CUTOFF_DATE" ]]; then
      log "  Pruning old failed image: $(basename "$OLD_IMAGE")"
      rm -f "$OLD_IMAGE"
      PRUNED=$((PRUNED + 1))
    fi
  done
  if (( PRUNED > 0 )); then
    log "  Pruned ${PRUNED} old image(s)."
  else
    log "  No images to prune."
  fi
fi

# ── Final status ─────────────────────────────────────────────────────────────
log "========================================"
if [[ "$TEST_PASSED" == true ]]; then
  log " RESULT: SUCCESS"
  log " Image:  ${IMAGE_FILE}.xz"
  log " Latest: ${LATEST_LINK}.xz"
else
  log " RESULT: FAILED"
  log " Check log: ${LOG_FILE}"
fi
log " Finished: $(date)"
log "========================================"

if [[ "$TEST_PASSED" == true ]]; then
  exit 0
else
  exit 1
fi
