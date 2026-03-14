# Migration PoC — Testing & Verification Guide

## Prerequisites

- Both nodes running v2 orchestrators (box=192.168.100.137, w1=192.168.100.253)
- WireGuard tunnel active: `wg0` between box (10.99.0.1) and w1 (10.99.0.2)
- Root SSH access between nodes via WireGuard IPs
- Codebase synced to both nodes at `/opt/e2b/infra/`

## 1. Unit Tests (no orchestrator needed)

These test the data models, validation, and nftables operations. Run on either node as root.

```bash
# SSH to w1 (or box)
ssh w1

# Run pure unit tests (no network infra needed)
cd /opt/e2b/infra/packages/orchestrator
sudo go test -v -run 'TestMigrationPreconditions|TestValidateBuildID|TestMigrationDomain' \
  ./internal/sandbox/network/v2/
```

Expected output:
```
=== RUN   TestMigrationPreconditions_Compatible
--- PASS: TestMigrationPreconditions_Compatible
=== RUN   TestValidateBuildID
--- PASS: TestValidateBuildID
=== RUN   TestMigrationDomain_States
--- PASS: TestMigrationDomain_States
=== RUN   TestMigrationDomain_Lifecycle
--- PASS: TestMigrationDomain_Lifecycle
```

## 2. nftables / IP Forwarding Tests (root, no orchestrator)

These test route and nftables rule creation/teardown. Require root + `wg0`.

```bash
# On w1 (has wg0)
cd /opt/e2b/infra/packages/orchestrator
sudo go test -v -run 'TestSetupIPForward|TestSetupMigrationDNAT' \
  ./internal/sandbox/network/v2/
```

Expected:
```
=== RUN   TestSetupIPForward
--- PASS: TestSetupIPForward
=== RUN   TestSetupIPForward_BadDevice
--- PASS: TestSetupIPForward_BadDevice
=== RUN   TestTeardownIPForward_NonexistentRoute
--- PASS: TestTeardownIPForward_NonexistentRoute
=== RUN   TestSetupMigrationDNAT
--- PASS: TestSetupMigrationDNAT
=== RUN   TestSetupMigrationDNAT_InvalidIPs
--- PASS: TestSetupMigrationDNAT_InvalidIPs
=== RUN   TestSetupMigrationDNAT_Idempotent
--- PASS: TestSetupMigrationDNAT_Idempotent
=== RUN   TestSetupMigrationDNAT_MultipleThenTeardown
--- PASS: TestSetupMigrationDNAT_MultipleThenTeardown
```

After tests, verify no leaked nftables rules:
```bash
sudo nft list tables | grep -v v2-host-firewall
# Should show no migration-related tables
```

## 3. IP Forwarding End-to-End Test (root + wg0 + both nodes)

This test verifies the route + DNAT plumbing without a full migration.

```bash
cd /opt/e2b/infra/packages/orchestrator
sudo MIGRATION_TEST=1 go test -v -run TestMigration_IPForwarding_EndToEnd \
  ./internal/sandbox/network/v2/
```

## 4. Manual Verification of IP Forwarding

If you want to verify the mechanism interactively:

```bash
# On w1 (source): add a test route
sudo ip route add 10.11.99.99/32 via 10.99.0.1 dev wg0

# Verify
ip route show 10.11.99.99/32
# Output: 10.11.99.99 via 10.99.0.1 dev wg0

# On box (target): add test DNAT
sudo nft add table inet test-migration
sudo nft add chain inet test-migration dnat_test '{ type nat hook prerouting priority -90; }'
sudo nft add rule inet test-migration dnat_test ip daddr 10.11.99.99 dnat to 10.11.0.5

# Verify
sudo nft list table inet test-migration

# Clean up
sudo ip route del 10.11.99.99/32       # on w1
sudo nft delete table inet test-migration  # on box
```

## 5. Full Migration Integration Test

**Requires**: Both orchestrators running, `wg0` active, root SSH between nodes.

First, check orchestrator gRPC ports. Default is 5008:
```bash
# On w1
ss -tlnp | grep 5008
# Should show orchestrator listening

# On box
ss -tlnp | grep 5008
```

Then find the actual cache directories the orchestrators use:
```bash
# Check what ORCHESTRATOR_BASE_PATH is set to
# Default: /orchestrator
# TemplateCacheDir = $ORCHESTRATOR_BASE_PATH/template
# DefaultCacheDir  = $ORCHESTRATOR_BASE_PATH/build

# On w1, check the env file:
cat /opt/e2b/infra/packages/orchestrator/env-v2.sh | grep -E 'BASE_PATH|CACHE'

# On box:
cat /tmp/orch_v2_env.sh | grep -E 'BASE_PATH|CACHE'
```

Run the test with the correct values:
```bash
cd /opt/e2b/infra/packages/orchestrator

# Migrate from w1 -> box
sudo MIGRATION_TEST=1 \
  MIGRATION_SOURCE_ADDR=localhost:5008 \
  MIGRATION_TARGET_ADDR=192.168.100.137:5008 \
  MIGRATION_SOURCE_WG_IP=10.99.0.2 \
  MIGRATION_TARGET_WG_IP=10.99.0.1 \
  MIGRATION_SOURCE_CACHE_DIR=/orchestrator/template \
  MIGRATION_TARGET_CACHE_DIR=/orchestrator/template \
  MIGRATION_SOURCE_DEFAULT_CACHE_DIR=/orchestrator/build \
  MIGRATION_TARGET_DEFAULT_CACHE_DIR=/orchestrator/build \
  MIGRATION_TEMPLATE_ID=base \
  go test -v -run TestMigration_CrossNode -timeout 3m \
  ./internal/sandbox/network/v2/
```

Expected output (if everything works):
```
=== RUN   TestMigration_CrossNode
    Creating sandbox on source node...
    Sandbox created: mig-sb-1710345678901
    Starting migration...
    Migration completed in 4.2s (downtime: 2.8s)
      Pause: 1.1s, Transfer: 1.5s, Resume: 1.6s
--- PASS: TestMigration_CrossNode (8.2s)
```

## 6. Demo Script

Build and run the demo for step-by-step output:

```bash
cd /opt/e2b/infra/packages/orchestrator

# Build
go build -o bin/migrate-demo ./cmd/migrate-demo/

# Run (from w1, migrating to box)
sudo ./bin/migrate-demo \
  --source=localhost:5008 \
  --target=192.168.100.137:5008 \
  --template=base \
  --source-wg-ip=10.99.0.2 \
  --target-wg-ip=10.99.0.1 \
  --source-cache=/orchestrator/template \
  --target-cache=/orchestrator/template \
  --source-diff-cache=/orchestrator/build \
  --target-diff-cache=/orchestrator/build \
  --cleanup=true
```

Expected output:
```
[   0s] Connecting to source (localhost:5008) and target (192.168.100.137:5008)...
[   0s] Connected to both orchestrators
[   0s] Creating sandbox on source (template=base)...
[   0s] Sandbox created: mig-demo-1710345678901
[   0s] Waiting for sandbox to initialize...
[ 3.0s] Capturing sandbox config from source...
[ 3.0s] Config captured (vcpu=2, ram=512MB, kernel=6.1.x)
[ 3.0s] Pausing sandbox on source...
[ 4.2s] Sandbox paused (took 1.2s)
[ 4.2s] Transferring snapshot files via rsync over WireGuard...
[ 5.8s] Snapshot transferred (took 1.6s)
[ 5.8s] Resuming sandbox on target...
[ 7.5s] Sandbox resumed on target: mig-demo-1710345678901 (took 1.7s)
[ 7.5s] Verifying sandbox on target...
[ 7.6s] Sandbox verified on target

=== Migration Summary ===
  Sandbox:      mig-demo-1710345678901 -> mig-demo-1710345678901
  Source:        localhost:5008
  Target:        192.168.100.137:5008
  Pause:         1.2s
  Transfer:      1.6s
  Resume:        1.7s
  Downtime:      ~2.9s (pause + resume, excl. transfer overhead)
  Total:         7.6s

[ 7.6s] Cleaning up: deleting sandbox on target...
[ 7.8s] Cleanup complete
[ 7.8s] Migration demo finished successfully!
```

## 7. Post-Migration Verification Checklist

After a successful migration, verify:

```bash
# 1. Sandbox running on target (box)
ssh box 'sudo ip netns list'
# Should see a new ns-N namespace

# 2. Sandbox NOT on source (w1)
ssh w1 'sudo ip netns list'
# The migrated sandbox's namespace should be gone

# 3. nftables clean on both nodes
ssh w1 'sudo nft list tables'
ssh box 'sudo nft list tables'
# Only v2-host-firewall should exist (no leftover migration tables)

# 4. No leaked routes
ssh w1 'ip route show | grep "10.11"'
# No migration-specific /32 routes unless IP forwarding is active

# 5. WireGuard tunnel healthy
ssh w1 'sudo wg show wg0'
ssh box 'sudo wg show wg0'
# Both should show recent handshake, transfer bytes
```

## 8. Troubleshooting

### "sandbox not found on source"
The sandbox may have already been paused/stopped. Check:
```bash
# List running sandboxes via gRPC
grpcurl -plaintext localhost:5008 orchestrator.SandboxService/List
```

### rsync fails
```bash
# Test SSH connectivity via WireGuard
ssh -o StrictHostKeyChecking=no root@10.99.0.1 echo ok

# Check if cache dirs exist
ls -la /orchestrator/template/
ls -la /orchestrator/build/
```

### "resume sandbox on target" fails
Check the target orchestrator logs. Common causes:
- Snapshot files not transferred correctly (check target cache dir)
- Insufficient VM resources (CPU/RAM) on target
- Template/kernel version not available on target

### nftables rule leak
If migration DNAT chains are left behind:
```bash
sudo nft delete chain inet v2-host-firewall migration_dnat
sudo nft delete chain inet v2-host-firewall migration_forward
```

### Route leak
```bash
# Remove specific migration route
sudo ip route del 10.11.X.X/32 dev wg0
```
