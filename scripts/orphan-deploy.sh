#!/bin/bash
# Deploy and monitor orphan reconciler
# Usage: ./orphan-deploy.sh <node-ip> [sweep-time]
# Example: ./orphan-deploy.sh 10.0.0.5 "18:20"

set -e

if [ $# -lt 1 ]; then
    echo "Usage: $0 <node-ip> [sweep-time]"
    echo "Example: $0 10.0.0.5 18:20"
    exit 1
fi

NODE_IP="$1"
SWEEP_TIME="${2:-18:20}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN_PATH="$SCRIPT_DIR/packages/orchestrator/bin/orchestrator"
REMOTE_BIN="/usr/local/bin/orchestrator"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

echo -e "${BLUE}=== Orphan Reconciler Deploy ===${NC}"
echo -e "Node: ${YELLOW}${NODE_IP}${NC}"
echo -e "Sweep time: ${YELLOW}${SWEEP_TIME}${NC}"
echo ""

# Step 1: Build
echo -e "${BLUE}[1/4] Building binary with sweep time ${SWEEP_TIME}...${NC}"
cd "$SCRIPT_DIR"
./scripts/debug-orphan.sh "$SWEEP_TIME" > /tmp/build.log 2>&1
if [ $? -ne 0 ]; then
    echo -e "${RED}✗ Build failed${NC}"
    tail -20 /tmp/build.log
    exit 1
fi
echo -e "${GREEN}✓ Binary ready${NC}"

# Step 2: Deploy
echo -e "${BLUE}[2/4] Deploying to ${NODE_IP}...${NC}"
if ! scp "$BIN_PATH" "root@${NODE_IP}:${REMOTE_BIN}" 2>&1 | grep -v "^$"; then
    echo -e "${RED}✗ Deploy failed${NC}"
    exit 1
fi
echo -e "${GREEN}✓ Binary deployed${NC}"

# Step 3: Restart service
echo -e "${BLUE}[3/4] Restarting orchestrator service...${NC}"
ssh "root@${NODE_IP}" "systemctl restart orchestrator" 2>&1 || true
sleep 2
echo -e "${GREEN}✓ Service restarted${NC}"

# Step 4: Verify
echo -e "${BLUE}[4/4] Verifying deployment...${NC}"
echo ""
echo -e "${YELLOW}Recent logs:${NC}"
ssh "root@${NODE_IP}" "journalctl -u orchestrator -n 20 --no-pager" 2>&1 | grep -E "orphan|sweep|reconciler" || echo "No orphan logs yet"

echo ""
echo -e "${GREEN}=== Deployment Complete ===${NC}"
echo -e "${YELLOW}Monitor logs:${NC}"
echo "  ssh root@${NODE_IP} journalctl -u orchestrator -f | grep orphan"
echo ""
echo -e "${YELLOW}Next sweep at: ${SWEEP_TIME}${NC}"
