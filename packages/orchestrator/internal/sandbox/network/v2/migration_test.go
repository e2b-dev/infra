package v2

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/emptypb"

	orchestrator "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

// skipIfNoMigrationEnv skips tests that need the full PoC lab setup.
func skipIfNoMigrationEnv(t *testing.T) {
	t.Helper()
	skipIfNotLinuxRoot(t)

	if os.Getenv("MIGRATION_TEST") != "1" {
		t.Skip("skipping: set MIGRATION_TEST=1 to run migration tests (requires PoC lab)")
	}
}

// migrationTestEnv loads lab configuration from environment variables.
type migrationTestEnv struct {
	sourceAddr            string // gRPC address of source orchestrator
	targetAddr            string // gRPC address of target orchestrator
	sourceWgIP            net.IP
	targetWgIP            net.IP
	wgDevice              string
	templateID            string
	sourceCacheDir        string
	targetCacheDir        string
	sourceDefaultCacheDir string
	targetDefaultCacheDir string
}

func loadMigrationTestEnv(t *testing.T) migrationTestEnv {
	t.Helper()

	env := migrationTestEnv{
		sourceAddr:            envOrDefault("MIGRATION_SOURCE_ADDR", "localhost:5008"),
		targetAddr:            envOrDefault("MIGRATION_TARGET_ADDR", "192.168.100.137:5008"),
		sourceWgIP:            net.ParseIP(envOrDefault("MIGRATION_SOURCE_WG_IP", "10.99.0.2")),
		targetWgIP:            net.ParseIP(envOrDefault("MIGRATION_TARGET_WG_IP", "10.99.0.1")),
		wgDevice:              envOrDefault("MIGRATION_WG_DEVICE", "wg0"),
		templateID:            envOrDefault("MIGRATION_TEMPLATE_ID", "base"),
		sourceCacheDir:        envOrDefault("MIGRATION_SOURCE_CACHE_DIR", "/orchestrator/template"),
		targetCacheDir:        envOrDefault("MIGRATION_TARGET_CACHE_DIR", "/orchestrator/template"),
		sourceDefaultCacheDir: envOrDefault("MIGRATION_SOURCE_DEFAULT_CACHE_DIR", "/orchestrator/build"),
		targetDefaultCacheDir: envOrDefault("MIGRATION_TARGET_DEFAULT_CACHE_DIR", "/orchestrator/build"),
	}

	require.NotNil(t, env.sourceWgIP, "invalid source WireGuard IP")
	require.NotNil(t, env.targetWgIP, "invalid target WireGuard IP")

	return env
}

func envOrDefault(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}

func TestMigrationPreconditions_Compatible(t *testing.T) {
	base := MigrationPreconditions{
		Architecture:       "x86_64",
		FirecrackerVersion: "1.6.0",
		HostKernelVersion:  "6.1.80",
		GuestImageABI:      "e2b-v2",
		CPUFamily:          "intel-icelake",
	}

	// Identical — should pass
	assert.NoError(t, base.Compatible(base))

	// Zero-value target — should pass (PoC mode, no enforcement)
	assert.NoError(t, base.Compatible(MigrationPreconditions{}))

	// Architecture mismatch
	bad := base
	bad.Architecture = "aarch64"
	err := base.Compatible(bad)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "architecture mismatch")

	// Firecracker version mismatch
	bad = base
	bad.FirecrackerVersion = "1.7.0"
	err = base.Compatible(bad)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "firecracker version mismatch")

	// Multiple mismatches reported together
	bad = base
	bad.Architecture = "aarch64"
	bad.HostKernelVersion = "5.15.0"
	err = base.Compatible(bad)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "architecture")
	assert.Contains(t, err.Error(), "host kernel")
}

func TestValidateBuildID(t *testing.T) {
	// Valid IDs
	assert.NoError(t, validateBuildID("abc-123"))
	assert.NoError(t, validateBuildID("mig-test-1234567890"))
	assert.NoError(t, validateBuildID("build_v2.3"))
	assert.NoError(t, validateBuildID("a"))

	// Invalid IDs
	assert.Error(t, validateBuildID(""), "empty should fail")
	assert.Error(t, validateBuildID("../../etc/passwd"), "path traversal should fail")
	assert.Error(t, validateBuildID("foo bar"), "spaces should fail")
	assert.Error(t, validateBuildID("foo;rm -rf /"), "semicolons should fail")
	assert.Error(t, validateBuildID("id\x00null"), "null bytes should fail")
	assert.Error(t, validateBuildID("$(cmd)"), "shell expansion should fail")
	assert.Error(t, validateBuildID("foo/bar"), "slashes should fail")
}

func TestMigrationDomain_States(t *testing.T) {
	d := DefaultMigrationDomain()
	assert.Equal(t, MigrationStateCompleted, d.State)
	assert.Zero(t, d.StartedAt)
	assert.False(t, d.ForwardViaWg)
}

func TestMigrationDomain_Lifecycle(t *testing.T) {
	d := &MigrationDomain{
		ID:         "mig-test-123",
		SourceNode: "w1:5009",
		TargetNode: "box:5009",
		SandboxID:  "sb-abc",
		BuildID:    "build-xyz",
		State:      MigrationStatePending,
		StartedAt:  time.Now(),
	}

	// Simulate state transitions
	d.State = MigrationStateActive
	d.PausedAt = time.Now()

	d.State = MigrationStateTransfer
	d.TransferAt = time.Now()

	d.State = MigrationStateResuming
	d.ResumedAt = time.Now()

	d.State = MigrationStateForwarding
	d.OldHostIP = net.ParseIP("10.11.0.2")
	d.NewHostIP = net.ParseIP("10.11.0.5")
	d.ForwardViaWg = true

	d.State = MigrationStateCompleted
	d.CompletedAt = time.Now()

	assert.Equal(t, MigrationStateCompleted, d.State)
	assert.True(t, d.ForwardViaWg)
	assert.True(t, d.CompletedAt.After(d.StartedAt))
	assert.True(t, d.ResumedAt.After(d.PausedAt))
}

func TestMigration_CrossNode(t *testing.T) {
	skipIfNoMigrationEnv(t)

	env := loadMigrationTestEnv(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	opts := []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}

	// Step 1: Create migration coordinator
	mc, err := NewMigrationCoordinator(ctx, env.sourceAddr, env.targetAddr, opts...)
	require.NoError(t, err, "should connect to both orchestrators")
	defer mc.Close()

	// Step 2: Create sandbox on source
	t.Log("Creating sandbox on source node...")
	buildID := fmt.Sprintf("mig-test-%d", time.Now().UnixMilli())
	sandboxID := fmt.Sprintf("mig-sb-%d", time.Now().UnixMilli())

	_, err = mc.sourceClient.Create(ctx, &orchestrator.SandboxCreateRequest{
		Sandbox: &orchestrator.SandboxConfig{
			TemplateId: env.templateID,
			SandboxId:  sandboxID,
			BuildId:    buildID,
		},
	})
	require.NoError(t, err, "should create sandbox on source")
	t.Logf("Sandbox created: %s", sandboxID)

	// Give the sandbox a moment to fully start
	time.Sleep(2 * time.Second)

	// Step 3: Verify sandbox is running on source
	// Note: RunningSandbox.ClientId is the orchestrator node ID, not sandbox ID.
	// Check sandbox ID via GetConfig().GetSandboxId().
	listResp, err := mc.sourceClient.List(ctx, &emptypb.Empty{})
	require.NoError(t, err)
	found := false
	for _, sb := range listResp.GetSandboxes() {
		if sb.GetConfig().GetSandboxId() == sandboxID {
			found = true
			break
		}
	}
	require.True(t, found, "sandbox should be listed on source")

	// Step 4: Execute migration
	t.Log("Starting migration...")
	migReq := MigrationRequest{
		SandboxID:             sandboxID,
		TemplateID:            env.templateID,
		BuildID:               buildID,
		SourceAddr:            env.sourceAddr,
		TargetAddr:            env.targetAddr,
		SourceWgIP:            env.sourceWgIP,
		TargetWgIP:            env.targetWgIP,
		WgDevice:              env.wgDevice,
		SourceCacheDir:        env.sourceCacheDir,
		TargetCacheDir:        env.targetCacheDir,
		SourceDefaultCacheDir: env.sourceDefaultCacheDir,
		TargetDefaultCacheDir: env.targetDefaultCacheDir,
		Timeout:               90 * time.Second,
	}

	result, err := mc.Migrate(ctx, migReq)
	require.NoError(t, err, "migration should succeed")

	t.Logf("Migration completed in %s (downtime: %s)", result.TotalDuration, result.DowntimeWindow)
	t.Logf("  Pause: %s, Transfer: %s, Resume: %s",
		result.PauseDuration, result.TransferDuration, result.ResumeDuration)

	// Step 5: Verify sandbox running on target
	targetList, err := mc.targetClient.List(ctx, &emptypb.Empty{})
	require.NoError(t, err)
	found = false
	for _, sb := range targetList.GetSandboxes() {
		if sb.GetConfig().GetSandboxId() == result.NewSandboxID {
			found = true
			break
		}
	}
	assert.True(t, found, "sandbox should be running on target after migration")

	// Step 6: Verify sandbox no longer on source
	sourceList, err := mc.sourceClient.List(ctx, &emptypb.Empty{})
	require.NoError(t, err)
	for _, sb := range sourceList.GetSandboxes() {
		assert.NotEqual(t, sandboxID, sb.GetConfig().GetSandboxId(),
			"sandbox should not be on source after migration")
	}

	// Step 7: Cleanup — delete sandbox on target
	_, err = mc.targetClient.Delete(ctx, &orchestrator.SandboxDeleteRequest{
		SandboxId: result.NewSandboxID,
	})
	assert.NoError(t, err, "should clean up sandbox on target")

	// Verify total downtime is reasonable (< 30s for PoC)
	assert.Less(t, result.DowntimeWindow.Seconds(), 30.0,
		"downtime should be under 30 seconds for PoC")
}

func TestMigration_IPForwarding_EndToEnd(t *testing.T) {
	skipIfNoMigrationEnv(t)
	skipIfNoWireGuard(t, "wg0")

	env := loadMigrationTestEnv(t)

	// This test verifies IP forwarding in isolation (without full migration).
	// It sets up a route + DNAT and checks with ip route.

	hf, err := NewHostFirewall("lo", testConfig())
	require.NoError(t, err)
	defer hf.Close()

	oldHostIP := net.ParseIP("10.11.99.80")
	newHostIP := net.ParseIP("10.11.0.10")

	// Setup forwarding (source side: route)
	err = SetupIPForward(oldHostIP, env.targetWgIP, env.wgDevice)
	require.NoError(t, err)

	// Setup DNAT (target side)
	err = SetupMigrationDNAT(hf, oldHostIP, newHostIP, env.wgDevice)
	require.NoError(t, err)

	// Verify route
	out, err := exec.Command("ip", "route", "show", "10.11.99.80/32").CombinedOutput()
	require.NoError(t, err)
	assert.Contains(t, string(out), "via "+env.targetWgIP.String())

	// Verify nftables DNAT
	out, err = exec.Command("nft", "list", "chain", "inet", hostFwTableName, migrationDNATChainName).CombinedOutput()
	require.NoError(t, err)
	nftOut := string(out)
	assert.Contains(t, nftOut, "dnat")

	// Teardown
	err = TeardownIPForward(oldHostIP, env.wgDevice)
	assert.NoError(t, err)
	err = TeardownMigrationDNAT(hf, oldHostIP)
	assert.NoError(t, err)

	// Verify cleanup
	out, err = exec.Command("ip", "route", "show", "10.11.99.80/32").CombinedOutput()
	require.NoError(t, err)
	assert.Empty(t, strings.TrimSpace(string(out)), "route should be cleaned up")
}
