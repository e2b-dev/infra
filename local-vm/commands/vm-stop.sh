#!/usr/bin/env bash
# Gracefully stop an E2B infrastructure VM (ACPI shutdown)
# Usage: e2b-local.sh stop [--name INSTANCE] [--force] [--all]
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/../lib/common.sh"

INSTANCE_NAME="default"
FORCE=false
STOP_ALL=false

while [[ $# -gt 0 ]]; do
  case $1 in
    --name)  INSTANCE_NAME="$2"; shift 2 ;;
    --force) FORCE=true; shift ;;
    --all)   STOP_ALL=true; shift ;;
    *) error "Unknown option: $1"; exit 1 ;;
  esac
done

stop_instance() {
  local NAME="$1"
  local STATE_DIR="/tmp/e2b-vm-${NAME}"
  local PID_FILE="${STATE_DIR}/pid"
  local MONITOR_SOCK="${STATE_DIR}/monitor.sock"
  local TAP_FILE="${STATE_DIR}/tap"

  info "Stopping E2B VM instance '$NAME'..."
  if [[ ! -d "$STATE_DIR" ]]; then
    warn "No state directory found for instance '$NAME'."
    return 1
  fi

  if [[ "$FORCE" == true ]]; then
    if [[ -f "$PID_FILE" ]]; then
      PID=$(cat "$PID_FILE")
      debug "Force-killing VM (PID: $PID)..."
      kill -9 "$PID" 2>/dev/null || true
    fi
  elif [[ -S "$MONITOR_SOCK" ]]; then
    debug "Sending ACPI shutdown..."
    echo "system_powerdown" | socat - UNIX-CONNECT:"$MONITOR_SOCK" 2>/dev/null || true
    if [[ -f "$PID_FILE" ]]; then
      PID=$(cat "$PID_FILE")
      for i in $(seq 1 60); do
        if ! kill -0 "$PID" 2>/dev/null; then break; fi
        if (( i == 60 )); then debug "Forcing..."; kill -9 "$PID" 2>/dev/null || true; fi
        sleep 2
      done
    fi
  else
    if [[ -f "$PID_FILE" ]]; then
      PID=$(cat "$PID_FILE")
      kill "$PID" 2>/dev/null || true; sleep 2; kill -9 "$PID" 2>/dev/null || true
    fi
  fi

  if [[ -f "$TAP_FILE" ]]; then
    TAP=$(cat "$TAP_FILE")
    if ip link show "$TAP" &>/dev/null; then
      debug "Removing TAP: $TAP..."
      ip link set "$TAP" down 2>/dev/null || true
      ip tuntap del "$TAP" mode tap 2>/dev/null || true
    fi
  fi

  rm -rf "$STATE_DIR"
  info "Instance '$NAME' stopped."
}

if [[ "$STOP_ALL" == true ]]; then
  FOUND=false
  for STATE_DIR in /tmp/e2b-vm-*/; do
    if [[ -d "$STATE_DIR" ]]; then
      NAME=$(basename "$STATE_DIR" | sed 's/^e2b-vm-//')
      stop_instance "$NAME"
      FOUND=true
    fi
  done
  if [[ "$FOUND" == false ]]; then info "No running E2B VM instances found."; fi
else
  stop_instance "$INSTANCE_NAME"
fi
