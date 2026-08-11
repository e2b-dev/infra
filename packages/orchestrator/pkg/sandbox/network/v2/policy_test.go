//go:build linux

package v2

import (
	"net"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPolicyRoute_Setup(t *testing.T) { //nolint:paralleltest // mutates host netns state: fixed-name dummy link "dummy-test", policy rule and route table 300
	skipIfNotLinuxRoot(t)

	ctx := t.Context()
	fwmark := uint32(0x300)
	tableID := 300

	// Setup: create a dummy device to route through
	err := exec.CommandContext(ctx, "ip", "link", "add", "dummy-test", "type", "dummy").Run()
	require.NoError(t, err)
	defer exec.CommandContext(ctx, "ip", "link", "del", "dummy-test").Run()

	err = exec.CommandContext(ctx, "ip", "link", "set", "dummy-test", "up").Run()
	require.NoError(t, err)

	err = exec.CommandContext(ctx, "ip", "addr", "add", "10.99.99.1/24", "dev", "dummy-test").Run()
	require.NoError(t, err)

	// Test setup
	gw := net.ParseIP("10.99.99.1")
	err = SetupPolicyRoute(fwmark, tableID, gw, "dummy-test")
	require.NoError(t, err)

	// Verify ip rule exists
	out, err := exec.CommandContext(ctx, "ip", "rule", "list").Output()
	require.NoError(t, err)
	assert.Contains(t, string(out), "lookup 300")

	// Verify route table has route
	out, err = exec.CommandContext(ctx, "ip", "route", "show", "table", "300").Output()
	require.NoError(t, err)
	assert.Contains(t, string(out), "dummy-test")

	// Teardown
	err = TeardownPolicyRoute(fwmark, tableID)
	require.NoError(t, err)

	// Verify rule is gone
	out, err = exec.CommandContext(ctx, "ip", "rule", "list").Output()
	require.NoError(t, err)
	for line := range strings.SplitSeq(string(out), "\n") {
		assert.NotContains(t, line, "lookup 300",
			"policy rule should be removed after teardown")
	}
}
