#!/usr/bin/env bash
# Show running E2B VM instances and their IPs
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/../lib/common.sh"

FOUND=false
for STATE_DIR in /tmp/e2b-vm-*/; do
  if [[ ! -d "$STATE_DIR" ]]; then continue; fi

  NAME=$(basename "$STATE_DIR" | sed 's/^e2b-vm-//')
  PID_FILE="${STATE_DIR}/pid"
  IP_FILE="${STATE_DIR}/ip"
  MAC_FILE="${STATE_DIR}/mac"

  PID=""; IP=""; MAC=""; STATUS="stopped"
  [[ -f "$PID_FILE" ]] && PID=$(cat "$PID_FILE" 2>/dev/null || true)
  [[ -f "$IP_FILE" ]]  && IP=$(cat "$IP_FILE" 2>/dev/null || true)
  [[ -f "$MAC_FILE" ]] && MAC=$(cat "$MAC_FILE" 2>/dev/null || true)

  if [[ -n "$PID" ]] && kill -0 "$PID" 2>/dev/null; then
    STATUS="running"
  fi

  if [[ "$FOUND" == false ]]; then
    printf "${C_BOLD}%-15s %-10s %-8s %-20s %s${C_RESET}\n" "INSTANCE" "STATUS" "PID" "IP" "MAC"
    printf "%-15s %-10s %-8s %-20s %s\n" "--------" "------" "---" "--" "---"
    FOUND=true
  fi

  if [[ "$STATUS" == "running" ]]; then
    STATUS_FMT="${C_GREEN}running${C_RESET}"
  else
    STATUS_FMT="${C_RED}stopped${C_RESET}"
  fi

  # printf with color needs manual padding since escape codes take width
  printf "%-15s " "$NAME"
  printf "${STATUS_FMT}"
  # Pad status column: "running"=7 chars, "stopped"=7 chars, column is 10
  printf "   "
  printf "%-8s %-20s %s\n" "${PID:-–}" "${IP:-–}" "${MAC:-–}"
done

if [[ "$FOUND" == false ]]; then
  info "No E2B VM instances found."
fi
