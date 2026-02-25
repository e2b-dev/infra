#!/usr/bin/env bash
set -euo pipefail
exec &>> /var/log/e2b-infra.log
echo "[$(date)] Starting E2B infrastructure..."

cd /opt/e2b/infra

# Generate clickhouse config if missing
if [[ ! -f packages/local-dev/clickhouse-config-generated.xml ]]; then
  USERNAME=clickhouse PASSWORD=clickhouse PORT=9000 \
    envsubst < packages/clickhouse/local/config.tpl.xml \
    > packages/local-dev/clickhouse-config-generated.xml
fi

# Start Docker Compose infrastructure
docker compose -f packages/local-dev/docker-compose.yaml up -d

# Wait for PostgreSQL
echo "[$(date)] Waiting for PostgreSQL..."
for i in $(seq 1 30); do
  if docker compose -f packages/local-dev/docker-compose.yaml \
       exec -T postgres pg_isready -U postgres &>/dev/null; then
    echo "[$(date)] PostgreSQL is ready."
    break
  fi
  sleep 2
done

source /opt/e2b/env.sh

# Ensure required directories exist
mkdir -p /opt/e2b/infra/packages/orchestrator/tmp/local-template-storage
mkdir -p /opt/e2b/infra/packages/orchestrator/tmp/sandbox-cache-dir
mkdir -p /opt/e2b/infra/packages/orchestrator/tmp/snapshot-cache

# Start API (port 80 for our VM setup, official default is 3000)
nohup /opt/e2b/infra/packages/api/bin/api --port 80 \
  >> /var/log/e2b-api.log 2>&1 &
echo "[$(date)] API started (PID: $!, port 80)"

# Start Orchestrator (runs from its package dir for relative paths)
cd /opt/e2b/infra/packages/orchestrator
nohup /opt/e2b/infra/packages/orchestrator/bin/orchestrator \
  >> /var/log/e2b-orchestrator.log 2>&1 &
echo "[$(date)] Orchestrator started (PID: $!, port 5008)"
cd /opt/e2b/infra

# Start Client-Proxy
nohup /opt/e2b/infra/packages/client-proxy/bin/client-proxy \
  >> /var/log/e2b-client-proxy.log 2>&1 &
echo "[$(date)] Client-Proxy started (PID: $!, port 3002)"

echo "[$(date)] E2B infrastructure started."
