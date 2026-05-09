#!/usr/bin/env bash
# Deploy orchestrator + envd binaries to PRIMARY (157.90.13.250) via SSH.
#
# Pattern: build → upload to Hetzner Object Storage → SSH-pull on PRIMARY → atomic-swap.
# Run from CI (GitHub Actions) after PR merge with HCLOUD_TOKEN + Object Storage creds.
#
# Sharky-Deploy workflow:
#   1. Build orchestrator + envd binaries (linux/amd64)
#   2. Upload to fc-versions bucket on Hetzner Object Storage
#   3. SSH to PRIMARY, pull latest binaries, atomic-swap symlink
#   4. systemctl restart orchestrator envd
#   5. Health-check :50051 + :49983

set -euo pipefail

PRIMARY_HOST="${PRIMARY_HOST:-157.90.13.250}"
SSH_USER="${SSH_USER:-root}"
SSH_KEY="${SSH_KEY:-$HOME/.ssh/id_ed25519}"
DEPLOY_VERSION="${DEPLOY_VERSION:-$(git rev-parse --short HEAD)}"
OBJECT_STORE_URL="${OBJECT_STORE_URL:-https://fsn1.your-objectstorage.com}"
FC_VERSIONS_BUCKET="${FC_VERSIONS_BUCKET:-maxicore-orch-fc-versions}"

REPO_ROOT="$(cd "$(dirname "$0")/../../../.." && pwd)"

echo "═══ NX.5 Deploy: orchestrator + envd → PRIMARY ($PRIMARY_HOST) ═══"
echo "Version: $DEPLOY_VERSION"
echo ""

# ─────────────────────────── 1. Build binaries (linux/amd64) ───────────────────────────

echo "[1/5] Building binaries..."
cd "$REPO_ROOT/packages/orchestrator"
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 \
    go build -o /tmp/orchestrator-bin -ldflags "-X main.commitSHA=$DEPLOY_VERSION" .

cd "$REPO_ROOT/packages/envd"
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 \
    go build -o /tmp/envd-bin -ldflags "-X main.commitSHA=$DEPLOY_VERSION" .

echo "  ✅ orchestrator: $(stat -c %s /tmp/orchestrator-bin) bytes"
echo "  ✅ envd: $(stat -c %s /tmp/envd-bin) bytes"

# ─────────────────────────── 2. Upload to Hetzner Object Storage ───────────────────────────

echo "[2/5] Upload to Hetzner Object Storage..."
mc alias set hetzner "$OBJECT_STORE_URL" "$HETZNER_OBJECT_STORAGE_ACCESS_KEY" "$HETZNER_OBJECT_STORAGE_SECRET_KEY"
mc cp /tmp/orchestrator-bin "hetzner/$FC_VERSIONS_BUCKET/orchestrator/$DEPLOY_VERSION"
mc cp /tmp/envd-bin "hetzner/$FC_VERSIONS_BUCKET/envd/$DEPLOY_VERSION"
echo "  ✅ Uploaded to $FC_VERSIONS_BUCKET/{orchestrator,envd}/$DEPLOY_VERSION"

# ─────────────────────────── 3. SSH to PRIMARY: pull + atomic-swap ───────────────────────────

echo "[3/5] SSH PRIMARY: download + atomic-swap..."
ssh -i "$SSH_KEY" -o StrictHostKeyChecking=accept-new "$SSH_USER@$PRIMARY_HOST" bash <<EOF
set -euo pipefail
# Setup mc on PRIMARY if missing
command -v mc >/dev/null || { curl -sfL https://dl.min.io/client/mc/release/linux-amd64/mc -o /usr/local/bin/mc; chmod +x /usr/local/bin/mc; }

mc alias set hetzner "$OBJECT_STORE_URL" "$HETZNER_OBJECT_STORAGE_ACCESS_KEY" "$HETZNER_OBJECT_STORAGE_SECRET_KEY"

# Download new binaries to staging
mkdir -p /opt/maxicore/orchestrator/bin /opt/maxicore/envd/bin /opt/maxicore/staging
mc cp "hetzner/$FC_VERSIONS_BUCKET/orchestrator/$DEPLOY_VERSION" /opt/maxicore/staging/orchestrator-$DEPLOY_VERSION
mc cp "hetzner/$FC_VERSIONS_BUCKET/envd/$DEPLOY_VERSION" /opt/maxicore/staging/envd-$DEPLOY_VERSION

chmod +x /opt/maxicore/staging/orchestrator-$DEPLOY_VERSION
chmod +x /opt/maxicore/staging/envd-$DEPLOY_VERSION

# Atomic swap (symlink-based)
ln -sfn /opt/maxicore/staging/orchestrator-$DEPLOY_VERSION /opt/maxicore/orchestrator/bin/orchestrator.new
ln -sfn /opt/maxicore/staging/envd-$DEPLOY_VERSION /opt/maxicore/envd/bin/envd.new
mv /opt/maxicore/orchestrator/bin/orchestrator.new /opt/maxicore/orchestrator/bin/orchestrator
mv /opt/maxicore/envd/bin/envd.new /opt/maxicore/envd/bin/envd

# Restart services
systemctl restart orchestrator
sleep 2
systemctl restart envd
sleep 2

# Verify
systemctl is-active orchestrator
systemctl is-active envd
EOF

echo "  ✅ Atomic-swap + restart complete"

# ─────────────────────────── 4. Health-Check ───────────────────────────

echo "[4/5] Health-check..."
sleep 3

# gRPC health-probe
ssh -i "$SSH_KEY" "$SSH_USER@$PRIMARY_HOST" \
    "ss -tlnp | grep -E '(50051|49983)'" || \
    { echo "❌ Services not listening"; exit 1; }
echo "  ✅ orchestrator :50051 + envd :49983 listening"

# ─────────────────────────── 5. Cleanup old versions (keep last 3) ───────────────────────────

echo "[5/5] Cleanup old versions on PRIMARY..."
ssh -i "$SSH_KEY" "$SSH_USER@$PRIMARY_HOST" bash <<'EOF'
cd /opt/maxicore/staging
ls -1t orchestrator-* 2>/dev/null | tail -n +4 | xargs -r rm -f
ls -1t envd-* 2>/dev/null | tail -n +4 | xargs -r rm -f
EOF
echo "  ✅ Old versions cleaned"

echo ""
echo "═══ NX.5 Deploy COMPLETE: $DEPLOY_VERSION on PRIMARY ═══"
