#!/bin/bash

# Start a service and/or wait for its health endpoint.
#
# Modes:
#   start-service.sh start <name> <make_path> <make_command> <log_file>
#       Kick the service off in the background and return immediately.
#   start-service.sh wait <name> <log_file> <health_url>
#       Poll the health URL until healthy (STARTUP_TIMEOUT, default 30s);
#       on timeout print the log tail and fail.
#   start-service.sh <name> <make_path> <make_command> <log_file> <health_url>
#       Legacy form: start + wait in one call.
#
# Splitting start from wait lets independent services boot concurrently:
# `start A; start B; wait A; wait B` boots B while A is being waited on.

set -uo pipefail

# Default timeout, override with STARTUP_TIMEOUT env
TIMEOUT=${STARTUP_TIMEOUT:-30}

start() {
    local name="$1" make_path="$2" make_command="$3" log_file="$4"
    echo "Starting $name..."
    make -C "$make_path" "$make_command" 2>&1 | tee "$log_file" &
}

wait_healthy() {
    local name="$1" log_file="$2" health_url="$3"
    echo "Waiting for $name to become healthy at $health_url (timeout: $TIMEOUT seconds)..."
    for ((i = 0; i < TIMEOUT; i++)); do
        if curl -s -o /dev/null -w "%{http_code}" "$health_url" | grep -q 200; then
            echo "$name is healthy and running."
            return 0
        fi
        sleep 1
    done
    echo "$name failed to become healthy in time. Last log lines:"
    tail -30 "$log_file" 2>/dev/null || true
    return 1
}

case "${1:-}" in
    start)
        if [ "$#" -ne 5 ]; then
            echo "Usage: $0 start <name> <make_path> <make_command> <log_file>"
            exit 1
        fi
        start "$2" "$3" "$4" "$5"
        ;;
    wait)
        if [ "$#" -ne 4 ]; then
            echo "Usage: $0 wait <name> <log_file> <health_url>"
            exit 1
        fi
        wait_healthy "$2" "$3" "$4"
        ;;
    *)
        if [ "$#" -ne 5 ]; then
            echo "Usage: $0 <name> <make_path> <make_command> <log_file> <health_url>"
            exit 1
        fi
        start "$1" "$2" "$3" "$4"
        wait_healthy "$1" "$4" "$5"
        ;;
esac
