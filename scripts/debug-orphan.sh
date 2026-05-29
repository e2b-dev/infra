#!/bin/bash
# Debug script for orphan reconciler
# Usage: ./debug-orphan.sh [sweep-time] [dry-run]
# Example: ./debug-orphan.sh "18:20" true

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ORPHAN_PKG="$SCRIPT_DIR/packages/orchestrator/pkg/orphan"
ORCHESTRATOR_DIR="$SCRIPT_DIR/packages/orchestrator"
BIN_DIR="$ORCHESTRATOR_DIR/bin"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Parse arguments
SWEEP_TIME="${1:-18:20}"
DRY_RUN="${2:-false}"

# Extract hour and minute from sweep time
IFS=':' read -r HOUR MINUTE <<< "$SWEEP_TIME"
HOUR=${HOUR:-18}
MINUTE=${MINUTE:-20}

echo -e "${BLUE}=== Orphan Reconciler Debug Script ===${NC}"
echo -e "Sweep time: ${YELLOW}${HOUR}:${MINUTE}${NC}"
echo -e "Dry run: ${YELLOW}${DRY_RUN}${NC}"
echo ""

# Step 1: Update reconciler.go
echo -e "${BLUE}[1/5] Updating reconciler.go...${NC}"
SWEEP_DURATION="${HOUR}*time.Hour + ${MINUTE}*time.Minute"

# Update the constant
sed -i "s/sweepTime = [0-9]*\*time\.Hour + [0-9]*\*time\.Minute/sweepTime = ${SWEEP_DURATION}/" "$ORPHAN_PKG/reconciler.go"

# Update comments
sed -i "s/at [0-9][0-9]:[0-9][0-9]/at ${HOUR}:${MINUTE}/g" "$ORPHAN_PKG/reconciler.go"

echo -e "${GREEN}✓ Updated reconciler.go${NC}"

# Step 2: Verify syntax
echo -e "${BLUE}[2/5] Verifying syntax...${NC}"
if ! go build ./pkg/orphan/... -C "$ORCHESTRATOR_DIR" 2>&1 | head -20; then
    echo -e "${RED}✗ Build failed${NC}"
    exit 1
fi
echo -e "${GREEN}✓ Syntax OK${NC}"

# Step 3: Run tests
echo -e "${BLUE}[3/5] Running unit tests...${NC}"
TEST_OUTPUT=$(go test -v -count=1 -race ./pkg/orphan/... -C "$ORCHESTRATOR_DIR" 2>&1 | tail -10)
if echo "$TEST_OUTPUT" | grep -q "PASS"; then
    echo -e "${GREEN}✓ All tests passed${NC}"
    echo "$TEST_OUTPUT" | tail -3
else
    echo -e "${RED}✗ Tests failed${NC}"
    echo "$TEST_OUTPUT"
    exit 1
fi

# Step 4: Verify time calculation
echo -e "${BLUE}[4/5] Verifying time calculation...${NC}"
cat > /tmp/verify_sweep.go << EOF
package main

import (
	"fmt"
	"time"
)

func nextSweepTime(now time.Time, sweepDuration time.Duration) time.Time {
	hour := int(sweepDuration.Hours())
	minute := int((sweepDuration % time.Hour).Minutes())

	y, m, d := now.Date()
	loc := now.Location()
	candidate := time.Date(y, m, d, hour, minute, 0, 0, loc)

	if !candidate.After(now) {
		candidate = candidate.Add(24 * time.Hour)
	}

	return candidate
}

func main() {
	sweepTime := ${HOUR}*time.Hour + ${MINUTE}*time.Minute
	
	testCases := []string{
		"2026-05-29 08:00:00",
		"2026-05-29 ${HOUR}:$(printf '%02d' $((MINUTE-1))):59",
		"2026-05-29 ${HOUR}:${MINUTE}:00",
		"2026-05-29 ${HOUR}:$(printf '%02d' $((MINUTE+1))):00",
		"2026-05-29 23:59:59",
	}
	
	for _, tc := range testCases {
		now, _ := time.ParseInLocation("2006-01-02 15:04:05", tc, time.Local)
		next := nextSweepTime(now, sweepTime)
		fmt.Printf("现在: %s → 下次清理: %s\n", now.Format("2006-01-02 15:04:05"), next.Format("2006-01-02 15:04:05"))
	}
}
EOF

go run /tmp/verify_sweep.go
echo -e "${GREEN}✓ Time calculation verified${NC}"

# Step 5: Build binary
echo -e "${BLUE}[5/5] Building orchestrator binary...${NC}"
mkdir -p "$BIN_DIR"
go build -o "$BIN_DIR/orchestrator" ./main.go -C "$ORCHESTRATOR_DIR"
BINARY_SIZE=$(du -h "$BIN_DIR/orchestrator" | cut -f1)
echo -e "${GREEN}✓ Binary built: ${BINARY_SIZE}${NC}"

# Summary
echo ""
echo -e "${GREEN}=== Debug Summary ===${NC}"
echo -e "Sweep time: ${YELLOW}${HOUR}:${MINUTE}${NC}"
echo -e "Binary: ${YELLOW}$BIN_DIR/orchestrator${NC}"
echo -e "Size: ${YELLOW}${BINARY_SIZE}${NC}"
echo ""

# Show current config
echo -e "${BLUE}Current configuration:${NC}"
grep -A 2 "sweepTime = " "$ORPHAN_PKG/reconciler.go" | head -3

echo ""
echo -e "${YELLOW}Next steps:${NC}"
echo "1. Deploy binary: scp $BIN_DIR/orchestrator <node>:/path/to/orchestrator"
echo "2. Restart orchestrator service"
echo "3. Check logs: journalctl -u orchestrator -f"
echo "4. Verify sweep: grep 'orphan reconciler' /var/log/orchestrator.log"
