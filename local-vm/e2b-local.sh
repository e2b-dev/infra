#!/usr/bin/env bash
# e2b-local.sh — CLI dispatcher for managing E2B VM instances
#
# Usage:
#   e2b-local.sh [--verbose|-v] [--quiet|-q] <command> [options]
#   e2b-local.sh start [--name N] [--disk FILE] [--port-forward] ...
#   e2b-local.sh stop [--name N] [--force] [--all]
#   e2b-local.sh ssh [--name N] [-- args...]
#   e2b-local.sh status
#   e2b-local.sh network setup
#   e2b-local.sh network teardown
#   e2b-local.sh test [VM_IP] [TEMPLATE_ID]
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

usage() {
  echo "Usage: e2b-local.sh [--verbose|-v] [--quiet|-q] <command> [options]"
  echo ""
  echo "Global flags:"
  echo "  -v, --verbose   Show detailed output for debugging"
  echo "  -q, --quiet     Suppress informational output"
  echo ""
  echo "Commands:"
  echo "  start             Start a VM instance"
  echo "  stop              Stop a VM instance"
  echo "  ssh               SSH into a VM instance"
  echo "  status            Show running VM instances"
  echo "  network setup     Set up bridge networking"
  echo "  network teardown  Tear down bridge networking"
  echo "  test              Run sandbox test"
  echo ""
  echo "Run 'e2b-local.sh <command> --help' for command-specific options."
}

# Parse global flags before the subcommand
while [[ $# -gt 0 ]]; do
  case "$1" in
    -v|--verbose) export E2B_VERBOSE=1; shift ;;
    -q|--quiet)   export E2B_QUIET=1; shift ;;
    -*)
      # Could be a subcommand flag (e.g. --help), break and let case handle it
      break
      ;;
    *)
      break
      ;;
  esac
done

if [[ $# -eq 0 ]]; then
  usage
  exit 1
fi

COMMAND="$1"
shift

case "$COMMAND" in
  start)
    exec "${SCRIPT_DIR}/commands/vm-start.sh" "$@"
    ;;
  stop)
    exec "${SCRIPT_DIR}/commands/vm-stop.sh" "$@"
    ;;
  ssh)
    exec "${SCRIPT_DIR}/commands/vm-ssh.sh" "$@"
    ;;
  status)
    exec "${SCRIPT_DIR}/commands/vm-status.sh" "$@"
    ;;
  network)
    if [[ $# -eq 0 ]]; then
      echo "Usage: e2b-local.sh network <setup|teardown>"
      exit 1
    fi
    SUBCMD="$1"
    shift
    case "$SUBCMD" in
      setup)    exec "${SCRIPT_DIR}/commands/network-setup.sh" "$@" ;;
      teardown) exec "${SCRIPT_DIR}/commands/network-teardown.sh" "$@" ;;
      *)        echo "Unknown network subcommand: $SUBCMD"; exit 1 ;;
    esac
    ;;
  test)
    # Install npm deps if needed
    if [[ ! -d "${SCRIPT_DIR}/node_modules" ]]; then
      (cd "$SCRIPT_DIR" && npm install --no-audit --no-fund 2>&1)
    fi
    exec node "${SCRIPT_DIR}/test-sandbox.mjs" "$@"
    ;;
  --help|-h|help)
    usage
    exit 0
    ;;
  *)
    echo "Unknown command: $COMMAND"
    usage
    exit 1
    ;;
esac
