#!/usr/bin/env bash
# SSH into an E2B VM instance
# Usage: e2b-local.sh ssh [--name INSTANCE] [--ip ADDRESS] [-- SSH_ARGS...]
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/../lib/common.sh"

INSTANCE_NAME="default"
VM_IP=""
EXTRA_ARGS=()

while [[ $# -gt 0 ]]; do
  case $1 in
    --name) INSTANCE_NAME="$2"; shift 2 ;;
    --ip)   VM_IP="$2"; shift 2 ;;
    --)     shift; EXTRA_ARGS=("$@"); break ;;
    *)      EXTRA_ARGS+=("$1"); shift ;;
  esac
done

if [[ -z "$VM_IP" ]]; then
  VM_IP=$(e2b_vm_ip "$INSTANCE_NAME")
  if [[ -z "$VM_IP" ]]; then
    error "No IP found for instance '$INSTANCE_NAME'. Is it running?"
    exit 1
  fi
fi

SSH_CMD=($SSH_OPTS)
[[ -f "$SSH_KEY" ]] && SSH_CMD+=(-i "$SSH_KEY")
[[ "$VM_IP" == "localhost" ]] && SSH_CMD+=(-p 2222)
exec ssh "${SSH_CMD[@]}" "e2b@${VM_IP}" "${EXTRA_ARGS[@]}"
