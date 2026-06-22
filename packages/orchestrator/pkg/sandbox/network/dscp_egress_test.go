//go:build linux

package network

import (
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/coreos/go-iptables/iptables"
	"github.com/stretchr/testify/require"
	"github.com/vishvananda/netns"
)

// TestCreateNetwork_TagsEgressWithDSCP verifies that CreateNetwork installs the
// mangle/POSTROUTING rule that stamps DSCP CS1 (8) on every packet leaving the
// sandbox netns through the vpeer uplink (eth0).
//
// It exercises the real CreateNetwork path, so it needs root (netns, iptables)
// and the xt_DSCP kernel module; it skips otherwise — same gating as the other
// privileged integration tests in the orchestrator (see cmd/smoketest).
func TestCreateNetwork_TagsEgressWithDSCP(t *testing.T) { //nolint:paralleltest // mutates the caller's netns via LockOSThread + netns.Set; cannot run in parallel
	if os.Geteuid() != 0 {
		t.Skip("requires root for netns + iptables")
	}

	config, err := ParseConfig()
	require.NoError(t, err)
	config.SandboxEgressDSCP = 8 // env default is 0 (disabled)

	const idx = 30000 // high, fixed: avoid collision with the pool's low-index Populate
	slot, err := NewSlot("dscp-egress-test", idx, config, NewNoopEgressProxy())
	require.NoError(t, err)

	require.NoError(t, slot.CreateNetwork(t.Context()))
	t.Cleanup(func() { _ = slot.RemoveNetwork() })

	rules := dscpMangleRules(t, slot.NamespaceID())

	require.Lenf(t, rules, 1, "want exactly one DSCP mangle rule in %s, got %v", slot.NamespaceID(), rules)
	require.Containsf(t, rules[0], "-o "+slot.VpeerName(), "DSCP rule must match the vpeer uplink: %s", rules[0])
	require.Containsf(t, rules[0], "--set-dscp 0x08", "DSCP rule must set CS1 (8): %s", rules[0])
}

// dscpMangleRules returns the mangle/POSTROUTING rules that reference the DSCP
// target inside the named network namespace.
func dscpMangleRules(t *testing.T, nsName string) []string {
	t.Helper()

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	host, err := netns.Get()
	require.NoError(t, err)
	defer func() {
		require.NoError(t, netns.Set(host))
		_ = host.Close()
	}()

	target, err := netns.GetFromName(nsName)
	require.NoError(t, err)
	defer func() { _ = target.Close() }()
	require.NoError(t, netns.Set(target))

	tables, err := iptables.New()
	require.NoError(t, err)
	all, err := tables.List("mangle", "POSTROUTING")
	require.NoError(t, err)

	var dscp []string
	for _, r := range all {
		if strings.Contains(r, "DSCP") {
			dscp = append(dscp, r)
		}
	}

	return dscp
}
