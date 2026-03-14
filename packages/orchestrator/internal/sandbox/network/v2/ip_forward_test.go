package v2

import (
	"net"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func skipIfNoWireGuard(t *testing.T, device string) {
	t.Helper()
	out, err := exec.Command("ip", "link", "show", device).CombinedOutput()
	if err != nil || !strings.Contains(string(out), device) {
		t.Skipf("skipping: WireGuard device %s not available", device)
	}
}

func TestSetupIPForward(t *testing.T) {
	skipIfNotLinuxRoot(t)
	skipIfNoWireGuard(t, "wg0")

	// Use a test IP in the sandbox range that's unlikely to conflict
	oldHostIP := net.ParseIP("10.11.99.99")
	targetWgIP := net.ParseIP("10.99.0.1")
	wgDevice := "wg0"

	// Setup
	err := SetupIPForward(oldHostIP, targetWgIP, wgDevice)
	require.NoError(t, err, "SetupIPForward should succeed")

	// Verify route exists
	out, err := exec.Command("ip", "route", "show", "10.11.99.99/32").CombinedOutput()
	require.NoError(t, err)
	assert.Contains(t, string(out), "10.11.99.99",
		"route for migrated IP should exist")
	assert.Contains(t, string(out), "via 10.99.0.1",
		"route should go via target WireGuard IP")
	assert.Contains(t, string(out), "dev wg0",
		"route should use wg0 device")

	// Teardown
	err = TeardownIPForward(oldHostIP, wgDevice)
	require.NoError(t, err, "TeardownIPForward should succeed")

	// Verify route removed
	out, err = exec.Command("ip", "route", "show", "10.11.99.99/32").CombinedOutput()
	require.NoError(t, err)
	assert.NotContains(t, string(out), "10.11.99.99",
		"route should be removed after teardown")
}

func TestSetupIPForward_BadDevice(t *testing.T) {
	skipIfNotLinuxRoot(t)

	oldHostIP := net.ParseIP("10.11.99.98")
	targetWgIP := net.ParseIP("10.99.0.1")

	err := SetupIPForward(oldHostIP, targetWgIP, "nonexistent0")
	assert.Error(t, err, "should fail with nonexistent device")
	assert.Contains(t, err.Error(), "find WireGuard device")
}

func TestTeardownIPForward_NonexistentRoute(t *testing.T) {
	skipIfNotLinuxRoot(t)
	skipIfNoWireGuard(t, "wg0")

	// Tearing down a route that doesn't exist should error
	oldHostIP := net.ParseIP("10.11.99.97")
	err := TeardownIPForward(oldHostIP, "wg0")
	assert.Error(t, err, "should fail when route doesn't exist")
}

func TestSetupMigrationDNAT(t *testing.T) {
	skipIfNotLinuxRoot(t)

	hf, err := NewHostFirewall("lo", testConfig())
	require.NoError(t, err, "should create host firewall")
	defer hf.Close()

	oldHostIP := net.ParseIP("10.11.99.50")
	newHostIP := net.ParseIP("10.11.0.5")

	// Setup DNAT
	err = SetupMigrationDNAT(hf, oldHostIP, newHostIP, "wg0")
	require.NoError(t, err, "SetupMigrationDNAT should succeed")

	// Verify nftables rules exist
	out, err := exec.Command("nft", "list", "table", "inet", hostFwTableName).CombinedOutput()
	require.NoError(t, err, "should list nftables table")
	nftOutput := string(out)
	assert.Contains(t, nftOutput, migrationDNATChainName,
		"migration DNAT chain should exist")
	assert.Contains(t, nftOutput, migrationForwardChainName,
		"migration forward chain should exist")

	// Teardown DNAT
	err = TeardownMigrationDNAT(hf, oldHostIP)
	require.NoError(t, err, "TeardownMigrationDNAT should succeed")

	// Verify chains removed
	out, err = exec.Command("nft", "list", "table", "inet", hostFwTableName).CombinedOutput()
	require.NoError(t, err)
	nftOutput = string(out)
	assert.NotContains(t, nftOutput, migrationDNATChainName,
		"migration DNAT chain should be removed")
}

func TestSetupMigrationDNAT_InvalidIPs(t *testing.T) {
	skipIfNotLinuxRoot(t)

	hf, err := NewHostFirewall("lo", testConfig())
	require.NoError(t, err)
	defer hf.Close()

	// nil IPs should fail
	err = SetupMigrationDNAT(hf, nil, net.ParseIP("10.11.0.5"), "wg0")
	assert.Error(t, err, "should fail with nil old IP")
}

func TestSetupMigrationDNAT_Idempotent(t *testing.T) {
	skipIfNotLinuxRoot(t)

	hf, err := NewHostFirewall("lo", testConfig())
	require.NoError(t, err)
	defer hf.Close()

	oldHostIP := net.ParseIP("10.11.99.60")

	// First setup
	err = SetupMigrationDNAT(hf, oldHostIP, net.ParseIP("10.11.0.6"), "wg0")
	require.NoError(t, err)
	assert.Len(t, hf.migrationRules, 1)

	// Second setup with same oldHostIP but different newHostIP — should replace
	err = SetupMigrationDNAT(hf, oldHostIP, net.ParseIP("10.11.0.7"), "wg0")
	require.NoError(t, err)
	assert.Len(t, hf.migrationRules, 1, "should still have 1 entry after replace")

	// Verify the new IP is in nftables, old one is not
	out, err := exec.Command("nft", "list", "table", "inet", hostFwTableName).CombinedOutput()
	require.NoError(t, err)
	nftOutput := string(out)
	assert.Contains(t, nftOutput, "10.11.0.7", "new target IP should be present")
	assert.NotContains(t, nftOutput, "10.11.0.6", "old target IP should be gone")

	// Cleanup
	err = TeardownMigrationDNAT(hf, oldHostIP)
	require.NoError(t, err)
}

func TestSetupMigrationDNAT_MultipleThenTeardown(t *testing.T) {
	skipIfNotLinuxRoot(t)

	hf, err := NewHostFirewall("lo", testConfig())
	require.NoError(t, err)
	defer hf.Close()

	// Add two migration DNAT rules
	err = SetupMigrationDNAT(hf, net.ParseIP("10.11.99.40"), net.ParseIP("10.11.0.4"), "wg0")
	require.NoError(t, err)

	err = SetupMigrationDNAT(hf, net.ParseIP("10.11.99.41"), net.ParseIP("10.11.0.5"), "wg0")
	require.NoError(t, err)

	// Verify both rules tracked
	assert.Len(t, hf.migrationRules, 2, "should track 2 migration entries")

	// Teardown first — chains should remain, but first IP's rule should be gone
	err = TeardownMigrationDNAT(hf, net.ParseIP("10.11.99.40"))
	require.NoError(t, err)

	assert.Len(t, hf.migrationRules, 1, "should track 1 migration entry after first removal")

	out, err := exec.Command("nft", "list", "table", "inet", hostFwTableName).CombinedOutput()
	require.NoError(t, err)
	nftOutput := string(out)
	assert.Contains(t, nftOutput, migrationDNATChainName,
		"chain should still exist while other DNAT active")
	// nft outputs match IPs in hex (@nh,128,32 0x...) but DNAT targets in
	// dotted decimal ("dnat ip to 10.11.0.X"). Check DNAT targets instead.
	assert.NotContains(t, nftOutput, "10.11.0.4",
		"first DNAT target should be removed from nftables")
	assert.Contains(t, nftOutput, "10.11.0.5",
		"second DNAT target should still be present")

	// Teardown second — chains should be removed
	err = TeardownMigrationDNAT(hf, net.ParseIP("10.11.99.41"))
	require.NoError(t, err)

	out, err = exec.Command("nft", "list", "table", "inet", hostFwTableName).CombinedOutput()
	require.NoError(t, err)
	assert.NotContains(t, string(out), migrationDNATChainName,
		"chain should be removed when all DNAT rules gone")
}
