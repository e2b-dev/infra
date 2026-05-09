#!/usr/bin/env bash
# Deploy Observability stack to Operator (or dedicated obs-server).
set -euo pipefail
DEPLOY_HOST="${DEPLOY_HOST:-178.105.7.48}"
SSH_USER="${SSH_USER:-root}"
DEPLOY_DIR="/opt/maxicore/observability"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

echo "═══ Observability Deploy → $DEPLOY_HOST ═══"

ssh "$SSH_USER@$DEPLOY_HOST" "mkdir -p $DEPLOY_DIR/secrets"
rsync -av --exclude='secrets' "$SCRIPT_DIR/" "$SSH_USER@$DEPLOY_HOST:$DEPLOY_DIR/"

# Create grafana_admin_password if missing
ssh "$SSH_USER@$DEPLOY_HOST" "[ -f $DEPLOY_DIR/secrets/grafana_admin_password ] || openssl rand -base64 32 > $DEPLOY_DIR/secrets/grafana_admin_password && chmod 600 $DEPLOY_DIR/secrets/grafana_admin_password"

ssh "$SSH_USER@$DEPLOY_HOST" "cd $DEPLOY_DIR && docker-compose pull && docker-compose up -d"
sleep 8

ssh "$SSH_USER@$DEPLOY_HOST" "curl -sf http://127.0.0.1:9090/-/healthy && echo ' ✅ Prometheus'"
ssh "$SSH_USER@$DEPLOY_HOST" "curl -sf http://127.0.0.1:3100/ready && echo ' ✅ Loki'"
ssh "$SSH_USER@$DEPLOY_HOST" "curl -sf http://127.0.0.1:3200/ready && echo ' ✅ Tempo'"

echo ""
echo "✅ Observability stack deployed"
echo "Grafana: ssh -L 3000:localhost:3000 $SSH_USER@$DEPLOY_HOST → http://localhost:3000"
