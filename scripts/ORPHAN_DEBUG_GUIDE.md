# Orphan Reconciler Debug Guide

Quick reference guide for debugging and deploying orphaned Firecracker process cleanup functionality.

## 📋 Quick Commands

### Local Development
```bash
# Debug cleanup logic for a specific time
./scripts/debug-orphan.sh "18:20"

# Run complete local test suite
./scripts/orphan-test-local.sh "18:20"

# Run unit tests only
cd packages/orchestrator && go test -v -race ./pkg/orphan/...

# Run specific test
cd packages/orchestrator && go test -v -run TestSweepTime ./pkg/orphan/...
```

### Deploy to Production
```bash
# Deploy to specific node (auto build, deploy, restart)
./scripts/orphan-deploy.sh 10.0.0.5 "18:20"

# View logs after deployment
ssh root@10.0.0.5 journalctl -u orchestrator -f | grep orphan

# View recent cleanup records
ssh root@10.0.0.5 journalctl -u orchestrator -n 100 --no-pager | grep "orphan reconciler"
```

## 🔍 Debug Workflows

### Scenario 1: Verify Time Calculation
```bash
# Run debug script to verify time calculation
./scripts/debug-orphan.sh "18:20"

# Example output:
# Now: 2026-05-29 08:00:00 → Next sweep: 2026-05-29 18:20:00
# Now: 2026-05-29 18:19:59 → Next sweep: 2026-05-29 18:20:00
# Now: 2026-05-29 18:20:00 → Next sweep: 2026-05-30 18:20:00
```

### Scenario 2: Change Cleanup Time
```bash
# Modify sweepTime in reconciler.go
# Then run debug script to verify
./scripts/debug-orphan.sh "17:30"

# Script will automatically:
# 1. Update reconciler.go
# 2. Verify syntax
# 3. Run all tests
# 4. Verify time calculation
# 5. Build binary
```

### Scenario 3: Quick Deploy
```bash
# One-click deploy to node
./scripts/orphan-deploy.sh 10.0.0.5 "18:20"

# Script will automatically:
# 1. Build binary
# 2. Deploy via SCP to node
# 3. Restart orchestrator service
# 4. Show recent logs
```

## 📊 Code Structure

```
packages/orchestrator/pkg/orphan/
├── types.go              # Data type definitions
├── detector.go           # Orphaned process detection logic
├── cleaner.go            # Cleanup logic
├── reconciler.go         # Main reconciler (contains sweepTime)
├── export_test.go        # Test helper functions
├── types_test.go         # Type tests
├── detector_test.go      # Detector tests
├── cleaner_test.go       # Cleaner tests
└── reconciler_test.go    # Reconciler tests
```

## 🧪 Test Coverage

All new files and methods have unit tests:

- **types_test.go**: Data type validation
- **detector_test.go**: Orphaned process/socket/FIFO/veth detection
- **cleaner_test.go**: Cleanup logic (process signals, socket deletion, iptables rules)
- **reconciler_test.go**: Time calculation, scan intervals, error handling

Run all tests:
```bash
cd packages/orchestrator
go test -v -race -count=1 ./pkg/orphan/...
```

## 🔧 FAQ

### Q: How do I verify the cleanup time is correct?
A: Run `./scripts/debug-orphan.sh "18:20"` and check the time calculation output.

### Q: How do I change the cleanup time?
A: 
1. Edit `packages/orchestrator/pkg/orphan/reconciler.go`
2. Modify the `sweepTime` variable
3. Run `./scripts/debug-orphan.sh "new-time"`

### Q: How do I deploy to production?
A: Run `./scripts/orphan-deploy.sh <node-ip> "cleanup-time"`

### Q: How do I view cleanup logs?
A: 
```bash
# Real-time view
ssh root@<node-ip> journalctl -u orchestrator -f | grep orphan

# View history
ssh root@<node-ip> journalctl -u orchestrator -n 100 --no-pager | grep "orphan reconciler"
```

### Q: How do I clean up only specific types of orphaned resources?
A: Edit the `Sweep()` method in `reconciler.go` and comment out unwanted cleanup logic.

## 📝 Log Examples

```
May 29 18:20:00 node orchestrator[1234]: orphan reconciler: starting sweep
May 29 18:20:01 node orchestrator[1234]: orphan reconciler: detected 5 orphaned processes
May 29 18:20:02 node orchestrator[1234]: orphan reconciler: detected 3 orphaned sockets
May 29 18:20:03 node orchestrator[1234]: orphan reconciler: detected 2 orphaned FIFOs
May 29 18:20:04 node orchestrator[1234]: orphan reconciler: detected 1 orphaned veth
May 29 18:20:05 node orchestrator[1234]: orphan reconciler: cleaned up 5 processes, 3 sockets, 2 FIFOs, 1 veth
May 29 18:20:06 node orchestrator[1234]: orphan reconciler: sweep completed in 6.123s
```

## 🚀 Next Steps

1. **Local test**: `./scripts/orphan-test-local.sh "18:20"`
2. **Verify time**: `./scripts/debug-orphan.sh "18:20"`
3. **Deploy**: `./scripts/orphan-deploy.sh <node-ip> "18:20"`
4. **Monitor**: `ssh root@<node-ip> journalctl -u orchestrator -f | grep orphan`

## 📞 Support

For issues, see:
- Test files: `packages/orchestrator/pkg/orphan/*_test.go`
- Implementation files: `packages/orchestrator/pkg/orphan/*.go`
- Integration point: `packages/orchestrator/internal/factories/run.go`
