//go:build linux

package v2

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateNetworkV2_FullLifecycle(t *testing.T) { //nolint:paralleltest // mutates host netns state: singleton nftables table "v2-host-firewall", named netns, veth links
	skipIfNotLinuxRoot(t)

	ctx := context.Background()

	// Create host firewall
	hf := newTestHostFirewall(t, testConfig())

	observer, err := NewVethObserver()
	require.NoError(t, err)
	defer observer.Close()

	slot := makeTestSlot(t, reserveNSTestIdx(t))
	sv2 := NewSlotV2(slot)

	// Create network
	require.NoError(t, CreateNetworkV2(ctx, slot, sv2, hf, observer))

	// Verify namespace exists
	out, err := exec.CommandContext(ctx, "ip", "netns", "list").Output()
	require.NoError(t, err)
	assert.Contains(t, string(out), slot.NamespaceID())

	// Verify veth exists in host
	out, err = exec.CommandContext(ctx, "ip", "link", "show", slot.VethName()).Output()
	require.NoError(t, err)
	assert.Contains(t, string(out), slot.VethName())

	// Teardown
	err = RemoveNetworkV2(ctx, slot, sv2, hf, observer)
	require.NoError(t, err)

	// Verify namespace is gone — check each line for exact match
	out, _ = exec.CommandContext(ctx, "ip", "netns", "list").Output()
	for line := range strings.SplitSeq(string(out), "\n") {
		assert.NotContains(t, line, slot.NamespaceID(),
			"namespace should be removed after teardown")
	}
}

func TestCreateNetworkV2_NoIptablesRules(t *testing.T) { //nolint:paralleltest // mutates host netns state: singleton nftables table "v2-host-firewall", named netns, veth links
	skipIfNotLinuxRoot(t)

	ctx := context.Background()

	hf := newTestHostFirewall(t, testConfig())

	slot := makeTestSlot(t, reserveNSTestIdx(t))
	sv2 := NewSlotV2(slot)

	require.NoError(t, CreateNetworkV2(ctx, slot, sv2, hf, nil))
	t.Cleanup(func() { _ = RemoveNetworkV2(ctx, slot, sv2, hf, nil) })

	// A conditional assertion would pass vacuously wherever iptables is
	// missing, which is exactly the host v2 targets.
	if _, err := exec.LookPath("iptables"); err != nil {
		t.Skip("iptables binary not on PATH; cannot prove the absence of iptables rules")
	}

	for _, chain := range []struct{ table, name string }{
		{"nat", "PREROUTING"},
		{"nat", "POSTROUTING"},
		{"filter", "FORWARD"},
	} {
		out, err := exec.CommandContext(ctx, "iptables", "-t", chain.table, "-L", chain.name, "-n").Output()
		require.NoError(t, err)
		assert.NotContainsf(t, string(out), slot.VethName(),
			"a v2 sandbox must not appear in iptables %s/%s", chain.table, chain.name)
	}
}

func TestCreateNetworkV2_CleanTeardown(t *testing.T) { //nolint:paralleltest // mutates host netns state: singleton nftables table "v2-host-firewall", named netns, veth links
	skipIfNotLinuxRoot(t)

	ctx := context.Background()

	hf := newTestHostFirewall(t, testConfig())

	observer, _ := NewVethObserver()
	defer observer.Close()

	slot := makeTestSlot(t, reserveNSTestIdx(t))
	sv2 := NewSlotV2(slot)

	require.NoError(t, CreateNetworkV2(ctx, slot, sv2, hf, observer))
	require.NoError(t, RemoveNetworkV2(ctx, slot, sv2, hf, observer))

	// Verify veth set is empty
	elements, err := hf.conn.GetSetElements(hf.vethSet)
	require.NoError(t, err)
	assert.Empty(t, elements, "veth set should be empty after teardown")

	// Verify no routes to the slot's host IP
	out, err := exec.CommandContext(ctx, "ip", "route", "show").Output()
	require.NoError(t, err)
	for line := range strings.SplitSeq(string(out), "\n") {
		assert.NotContains(t, line, slot.HostIPString(),
			"should have no route to slot host IP after teardown")
	}
}
