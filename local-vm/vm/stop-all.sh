#!/usr/bin/env bash
set -euo pipefail
echo "[$(date)] Stopping E2B infrastructure..."

cd /opt/e2b/infra

# Stop application processes
pkill -f "bin/api" || true
pkill -f "bin/orchestrator" || true
pkill -f "bin/client-proxy" || true

# Stop Docker Compose infrastructure
docker compose -f packages/local-dev/docker-compose.yaml down || true

echo "[$(date)] E2B infrastructure stopped."
