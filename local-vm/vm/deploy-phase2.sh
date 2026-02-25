#!/usr/bin/env bash
set -euo pipefail
exec &> >(tee -a /var/log/e2b-phase2.log)
echo "[$(date)] ═══ Phase 2: Running official dev setup ═══"

export DEBIAN_FRONTEND=noninteractive
export NEEDRESTART_MODE=a
export NEEDRESTART_SUSPEND=1
export HOME=/root
export PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin:/snap/bin:/usr/local/go/bin

# Verify we're on kernel 6.8+
KVER=$(uname -r)
KMAJOR=$(echo "$KVER" | cut -d. -f1)
KMINOR=$(echo "$KVER" | cut -d. -f2)
echo "[$(date)] Running on kernel: $KVER (major=$KMAJOR, minor=$KMINOR)"

if [[ "$KMAJOR" -lt 6 ]] || { [[ "$KMAJOR" -eq 6 ]] && [[ "$KMINOR" -lt 8 ]]; }; then
  echo "[$(date)] ERROR: Kernel $KVER is < 6.8. Template building requires 6.8+."
  echo "[$(date)] HWE kernel may not have been installed or GRUB not configured."
  echo "PHASE2_FAILED_KERNEL" > /var/log/e2b-build-status
  exit 1
fi

# Ensure Docker is running
systemctl start docker || true
sleep 5

# Patch seed-local-database.go to use fixed readable test keys
# Keys are hex strings: 32 chars (16 bytes) for local-dev, 40 chars (20 bytes) for seed-db.go
cd /opt/e2b/infra
echo "[$(date)] Patching seed files for test API keys..."
SEED_LOCAL="packages/local-dev/seed-local-database.go"
if [[ -f "$SEED_LOCAL" ]]; then
  sed -i 's/userTokenValue = "89215020937a4c989cde33d7bc647715"/userTokenValue = "00000000000000000000000000000000"/' "$SEED_LOCAL"
  sed -i 's/teamTokenValue = "53ae1fed82754c17ad8077fbc8bcdd90"/teamTokenValue = "00000000000000000000000000000000"/' "$SEED_LOCAL"
  echo "[$(date)] Patched $SEED_LOCAL"
else
  echo "[$(date)] WARNING: $SEED_LOCAL not found"
fi

# Pre-pull Docker images with retry (most common failure point)
if [[ -f packages/local-dev/docker-compose.yaml ]]; then
  echo "[$(date)] Pre-pulling Docker images (with retries)..."
  for ATTEMPT in 1 2 3; do
    echo "[$(date)] Docker pull attempt $ATTEMPT/3..."
    if docker compose -f packages/local-dev/docker-compose.yaml pull 2>&1; then
      echo "[$(date)] Docker images pulled successfully."
      break
    else
      echo "[$(date)] Docker pull attempt $ATTEMPT failed."
      if (( ATTEMPT < 3 )); then
        echo "[$(date)] Waiting 30s before retry..."
        sleep 30
      else
        echo "[$(date)] WARNING: Docker pull failed after 3 attempts. Continuing anyway..."
      fi
    fi
  done
fi

# ── Official make targets ──────────────────────────────────────────────

# Install gsutil (needed for downloading Firecracker binaries & kernels)
if ! command -v gsutil &>/dev/null; then
  echo "[$(date)] Installing google-cloud-cli for gsutil..."
  curl -fsSL https://packages.cloud.google.com/apt/doc/apt-key.gpg \
    | gpg --dearmor -o /usr/share/keyrings/cloud.google.gpg
  echo "deb [signed-by=/usr/share/keyrings/cloud.google.gpg] https://packages.cloud.google.com/apt cloud-sdk main" \
    > /etc/apt/sources.list.d/google-cloud-sdk.list
  apt-get update -qq && apt-get install -y -qq google-cloud-cli
fi

# Install envsubst (needed for clickhouse config generation)
if ! command -v envsubst &>/dev/null; then
  apt-get install -y -qq gettext-base
fi

# 1. Download Firecracker binaries and kernels
echo "[$(date)] Downloading Firecracker binaries..."
make download-public-firecrackers

echo "[$(date)] Downloading Firecracker kernels..."
make download-public-kernels

# 2. Generate clickhouse config (prerequisite for docker compose)
echo "[$(date)] Generating ClickHouse config..."
USERNAME=clickhouse PASSWORD=clickhouse PORT=9000 \
  envsubst < packages/clickhouse/local/config.tpl.xml \
  > packages/local-dev/clickhouse-config-generated.xml

# 3. Start Docker infrastructure (detached for build)
echo "[$(date)] Starting Docker infrastructure..."
docker compose -f packages/local-dev/docker-compose.yaml up -d

# Wait for PostgreSQL to be ready
echo "[$(date)] Waiting for PostgreSQL..."
for i in $(seq 1 60); do
  if docker compose -f packages/local-dev/docker-compose.yaml \
       exec -T postgres pg_isready -U postgres &>/dev/null; then
    echo "[$(date)] PostgreSQL is ready."
    break
  fi
  if (( i % 10 == 0 )); then echo "[$(date)] Still waiting for PostgreSQL... (${i}s)"; fi
  sleep 2
done

# Wait for ClickHouse to be ready
echo "[$(date)] Waiting for ClickHouse..."
for i in $(seq 1 30); do
  if docker compose -f packages/local-dev/docker-compose.yaml \
       exec -T clickhouse clickhouse-client --query 'SELECT 1' &>/dev/null; then
    echo "[$(date)] ClickHouse is ready."
    break
  fi
  sleep 2
done

# 4. Database migrations
echo "[$(date)] Running PostgreSQL migrations..."
make -C packages/db migrate-local

echo "[$(date)] Running ClickHouse migrations..."
make -C packages/clickhouse migrate-local

# 5. Build envd (sandbox agent)
echo "[$(date)] Building envd..."
make -C packages/envd build

# 6. Seed database (creates test user/team/tokens)
echo "[$(date)] Seeding database..."
make -C packages/local-dev seed-database

# 7. Build service binaries
echo "[$(date)] Building API..."
make -C packages/api build-debug

echo "[$(date)] Building Orchestrator..."
make -C packages/orchestrator build-local

echo "[$(date)] Building Client-Proxy..."
make -C packages/client-proxy build-debug

# 8. Write .env.local for shared/scripts (needed by make local-build-base-template)
cat > packages/shared/scripts/.env.local <<'SCRIPTENV'
E2B_ACCESS_TOKEN=sk_e2b_00000000000000000000000000000000
E2B_API_KEY=e2b_00000000000000000000000000000000
E2B_API_URL=http://localhost:80
SCRIPTENV

# 9. Start services temporarily for template building
echo "[$(date)] Starting services for template build..."

source /opt/e2b/env.sh

# Create required directories
mkdir -p /opt/e2b/infra/packages/orchestrator/tmp/local-template-storage
mkdir -p /opt/e2b/infra/packages/orchestrator/tmp/sandbox-cache-dir
mkdir -p /opt/e2b/infra/packages/orchestrator/tmp/snapshot-cache

# Start API on port 80 (our VM-specific override)
nohup /opt/e2b/infra/packages/api/bin/api --port 80 \
  >> /var/log/e2b-api.log 2>&1 &
API_PID=$!
echo "[$(date)] API started (PID: $API_PID, port 80)"

# Start Orchestrator (needs root for Firecracker)
cd /opt/e2b/infra/packages/orchestrator
nohup /opt/e2b/infra/packages/orchestrator/bin/orchestrator \
  >> /var/log/e2b-orchestrator.log 2>&1 &
ORCH_PID=$!
echo "[$(date)] Orchestrator started (PID: $ORCH_PID, port 5008)"
cd /opt/e2b/infra

# Start Client-Proxy
nohup /opt/e2b/infra/packages/client-proxy/bin/client-proxy \
  >> /var/log/e2b-client-proxy.log 2>&1 &
PROXY_PID=$!
echo "[$(date)] Client-Proxy started (PID: $PROXY_PID, port 3002)"

# Wait for services to initialize
echo "[$(date)] Waiting for services to start..."
sleep 15

# 10. Build base template
echo "[$(date)] Building base template..."
make -C packages/shared/scripts local-build-base-template || {
  echo "[$(date)] WARNING: Base template build failed. Will retry once..."
  sleep 10
  make -C packages/shared/scripts local-build-base-template || \
    echo "[$(date)] WARNING: Base template build failed on retry."
}

# Stop the temporary service processes (they'll be restarted by systemd on next boot)
echo "[$(date)] Stopping temporary service processes..."
kill $API_PID $ORCH_PID $PROXY_PID 2>/dev/null || true
sleep 3
docker compose -f packages/local-dev/docker-compose.yaml down || true
sync; sync

# Create envd symlink (belt and suspenders — create-build falls back
# to /opt/e2b/envd/bin/envd if HOST_ENVD_PATH not set)
mkdir -p /opt/e2b/envd/bin
if [[ -f /opt/e2b/infra/packages/envd/bin/envd ]]; then
  ln -sf /opt/e2b/infra/packages/envd/bin/envd /opt/e2b/envd/bin/envd
  echo "[$(date)] Created envd symlink at /opt/e2b/envd/bin/envd"
fi

# Enable e2b-infra.service for subsequent boots
systemctl daemon-reload
systemctl enable e2b-infra.service

# Disable the phase2 service (one-time only)
systemctl disable e2b-phase2.service || true
rm -f /opt/e2b/needs-phase2

echo "PHASE2_COMPLETE" > /var/log/e2b-build-status
echo "[$(date)] ═══ Phase 2 complete. E2B infrastructure ready. ═══"
