#!/usr/bin/env bash
# Deploy APISIX Gateway to Operator (or any Hetzner Cloud Server with Docker).
#
# 1:1 Manus pattern: APISIX 3.11.0 (verified live on api.manus.im).
# Deployment: Docker Compose with etcd + APISIX + Dashboard.

set -euo pipefail

DEPLOY_HOST="${DEPLOY_HOST:-178.105.7.48}"   # Operator default
SSH_USER="${SSH_USER:-root}"
SSH_KEY="${SSH_KEY:-$HOME/.ssh/id_ed25519}"
DEPLOY_DIR="${DEPLOY_DIR:-/opt/maxicore/apisix}"

REPO_ROOT="$(cd "$(dirname "$0")/../../../../.." && pwd)"
APISIX_DIR="$REPO_ROOT/iac/provider-hetzner/services/apisix"

echo "═══ APISIX Gateway Deploy → $DEPLOY_HOST ═══"
echo ""

# ─────────────────────────── 1. Upload configs ───────────────────────────

echo "[1/4] Upload docker-compose.yml + configs..."
ssh -i "$SSH_KEY" "$SSH_USER@$DEPLOY_HOST" "mkdir -p $DEPLOY_DIR/{config,logs}"
scp -i "$SSH_KEY" "$APISIX_DIR/docker-compose.yml" "$SSH_USER@$DEPLOY_HOST:$DEPLOY_DIR/"
scp -i "$SSH_KEY" -r "$APISIX_DIR/config/" "$SSH_USER@$DEPLOY_HOST:$DEPLOY_DIR/"
echo "  ✅ Configs uploaded"

# ─────────────────────────── 2. Pull + start APISIX ───────────────────────────

echo "[2/4] Docker pull + start..."
ssh -i "$SSH_KEY" "$SSH_USER@$DEPLOY_HOST" bash <<EOF
set -euo pipefail
cd $DEPLOY_DIR
docker-compose pull
docker-compose up -d
EOF
echo "  ✅ APISIX + etcd + Dashboard started"

# ─────────────────────────── 3. Health-Check ───────────────────────────

echo "[3/4] Health-check..."
sleep 8

# Check APISIX HTTP gateway
ssh -i "$SSH_KEY" "$SSH_USER@$DEPLOY_HOST" \
    "curl -sf http://127.0.0.1:9080/apisix/prometheus/metrics | head -1" >/dev/null \
    && echo "  ✅ APISIX :9080 healthy" \
    || { echo "  ❌ APISIX not responding"; exit 1; }

# Check etcd
ssh -i "$SSH_KEY" "$SSH_USER@$DEPLOY_HOST" \
    "docker exec maxicore-apisix-etcd etcdctl endpoint health" \
    && echo "  ✅ etcd healthy"

# ─────────────────────────── 4. Configure UFW (allow 9080+9443 from LB only) ───────────────────────────

echo "[4/4] UFW rules (allow 9080+9443 from internal network only)..."
ssh -i "$SSH_KEY" "$SSH_USER@$DEPLOY_HOST" bash <<'EOF'
ufw allow from 10.0.1.0/24 to any port 9080 comment "APISIX HTTP from cluster"
ufw allow from 10.0.1.0/24 to any port 9443 comment "APISIX HTTPS from cluster"
ufw allow from 10.10.0.0/24 to any port 9080 comment "APISIX HTTP from vSwitch"
ufw allow from 10.10.0.0/24 to any port 9443 comment "APISIX HTTPS from vSwitch"
# Admin API (9180) ONLY local
ufw deny 9180 comment "APISIX Admin LOCAL ONLY"
# Dashboard (9000) ONLY local (SSH tunnel for access)
ufw deny 9000 comment "APISIX Dashboard LOCAL ONLY (SSH tunnel)"
ufw reload || true
EOF
echo "  ✅ UFW rules applied"

echo ""
echo "═══ APISIX Deploy COMPLETE ═══"
echo ""
echo "Access:"
echo "  HTTP gateway:  http://$DEPLOY_HOST:9080 (from internal network only)"
echo "  HTTPS gateway: https://$DEPLOY_HOST:9443"
echo "  Dashboard:     ssh -L 9000:localhost:9000 $SSH_USER@$DEPLOY_HOST → http://localhost:9000"
echo "  Metrics:       http://$DEPLOY_HOST:9091/apisix/prometheus/metrics"
