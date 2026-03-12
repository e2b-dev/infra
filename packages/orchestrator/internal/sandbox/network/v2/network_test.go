package v2

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateNetworkV2_FullLifecycle(t *testing.T) {
	skipIfNotLinuxRoot(t)

	ctx := context.Background()

	// Create host firewall
	hf, err := NewHostFirewall("lo", testConfig())
	require.NoError(t, err)
	defer hf.Close()

	observer, err := NewVethObserver()
	require.NoError(t, err)
	defer observer.Close()

	// Use high index to avoid conflicts with running orchestrator's namespaces
	slot := makeTestSlot(t, 30001)
	sv2 := NewSlotV2(slot)

	// Create network
	err = CreateNetworkV2(ctx, slot, sv2, hf, observer)
	require.NoError(t, err)

	// Verify namespace exists
	out, err := exec.Command("ip", "netns", "list").Output()
	require.NoError(t, err)
	assert.Contains(t, string(out), slot.NamespaceID())

	// Verify veth exists in host
	out, err = exec.Command("ip", "link", "show", slot.VethName()).Output()
	require.NoError(t, err)
	assert.Contains(t, string(out), slot.VethName())

	// Teardown
	err = RemoveNetworkV2(ctx, slot, sv2, hf, observer)
	assert.NoError(t, err)

	// Verify namespace is gone — check each line for exact match
	out, _ = exec.Command("ip", "netns", "list").Output()
	for _, line := range strings.Split(string(out), "\n") {
		assert.NotContains(t, line, slot.NamespaceID(),
			"namespace should be removed after teardown")
	}
}

func TestCreateNetworkV2_NoIptablesRules(t *testing.T) {
	skipIfNotLinuxRoot(t)

	ctx := context.Background()

	hf, err := NewHostFirewall("lo", testConfig())
	require.NoError(t, err)
	defer hf.Close()

	slot := makeTestSlot(t, 30002)
	sv2 := NewSlotV2(slot)

	err = CreateNetworkV2(ctx, slot, sv2, hf, nil)
	require.NoError(t, err)
	defer RemoveNetworkV2(ctx, slot, sv2, hf, nil)

	// Verify NO iptables rules reference this veth
	out, err := exec.Command("iptables", "-t", "nat", "-L", "PREROUTING", "-n").Output()
	if err == nil {
		assert.NotContains(t, string(out), slot.VethName(),
			"v2 sandbox should not create iptables rules")
	}

	out, err = exec.Command("iptables", "-t", "filter", "-L", "FORWARD", "-n").Output()
	if err == nil {
		assert.NotContains(t, string(out), slot.VethName(),
			"v2 sandbox should not create iptables FORWARD rules")
	}
}

func TestCreateNetworkV2_CleanTeardown(t *testing.T) {
	skipIfNotLinuxRoot(t)

	ctx := context.Background()

	hf, err := NewHostFirewall("lo", testConfig())
	require.NoError(t, err)
	defer hf.Close()

	observer, _ := NewVethObserver()
	defer observer.Close()

	slot := makeTestSlot(t, 30003)
	sv2 := NewSlotV2(slot)

	err = CreateNetworkV2(ctx, slot, sv2, hf, observer)
	require.NoError(t, err)

	err = RemoveNetworkV2(ctx, slot, sv2, hf, observer)
	require.NoError(t, err)

	// Verify veth set is empty
	elements, err := hf.conn.GetSetElements(hf.vethSet)
	require.NoError(t, err)
	assert.Equal(t, 0, len(elements), "veth set should be empty after teardown")

	// Verify no routes to the slot's host IP
	out, err := exec.Command("ip", "route", "show").Output()
	require.NoError(t, err)
	lines := strings.Split(string(out), "\n")
	for _, line := range lines {
		assert.NotContains(t, line, slot.HostIPString(),
			"should have no route to slot host IP after teardown")
	}
}
