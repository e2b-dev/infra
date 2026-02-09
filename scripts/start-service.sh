#!/bin/bash

# Default timeout, override with STARTUP_TIMEOUT env
TIMEOUT=${STARTUP_TIMEOUT:-30}

if [ "$#" -ne 5 ]; then
    echo "Usage: $0 <name> <make_path> <make_command> <log_file> <health_url>"
    exit 1
fi

NAME="$1"
MAKE_PATH="$2"
MAKE_COMMAND="$3"
LOG_FILE="$4"
HEALTH_URL="$5"

echo "Starting $NAME..."
make -C "$MAKE_PATH" "$MAKE_COMMAND" 2>&1 | tee "$LOG_FILE" &

echo "Waiting for $NAME to become healthy at $HEALTH_URL (timeout: $TIMEOUT seconds)..."
for ((i = 0; i < TIMEOUT; i++)); do
    if curl -s -o /dev/null -w "%{http_code}" "$HEALTH_URL" | grep -q 200; then
        echo "$NAME is healthy and running."
        exit 0
    fi
    sleep 1
done

echo "$NAME failed to become healthy in time."
exit 1