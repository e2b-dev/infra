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

func dscp(v uint8) *uint8 { return &v }

// TestConfig_EgressDSCP covers how the two configured classes resolve. Build
// sandboxes get their own value when one is set and otherwise inherit the
// sandbox value, so a deployment that only sets SANDBOX_EGRESS_DSCP is
// unaffected by this split.
func TestConfig_EgressDSCP(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		config      Config
		wantSandbox uint8
		wantBuild   uint8
	}{
		{
			name:        "unset build value falls back to the sandbox value",
			config:      Config{SandboxEgressDSCP: 8},
			wantSandbox: 8,
			wantBuild:   8,
		},
		{
			name:        "build value overrides for builds only",
			config:      Config{SandboxEgressDSCP: 8, BuildSandboxEgressDSCP: dscp(16)},
			wantSandbox: 8,
			wantBuild:   16,
		},
		{
			name:        "explicit zero disables marking for builds only",
			config:      Config{SandboxEgressDSCP: 8, BuildSandboxEgressDSCP: dscp(0)},
			wantSandbox: 8,
			wantBuild:   0,
		},
		{
			name:        "builds can be marked while sandboxes are not",
			config:      Config{SandboxEgressDSCP: 0, BuildSandboxEgressDSCP: dscp(16)},
			wantSandbox: 0,
			wantBuild:   16,
		},
		{
			name:        "both disabled by default",
			config:      Config{},
			wantSandbox: 0,
			wantBuild:   0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tt.wantSandbox, tt.config.EgressDSCP(EgressClassSandbox))
			require.Equal(t, tt.wantBuild, tt.config.EgressDSCP(EgressClassBuild))
		})
	}
}

func TestConfig_Validate_EgressDSCPRange(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		config  Config
		wantErr string
	}{
		{name: "both unset", config: Config{}},
		{name: "both at the top of the range", config: Config{SandboxEgressDSCP: 63, BuildSandboxEgressDSCP: dscp(63)}},
		{
			name:    "sandbox value out of range",
			config:  Config{SandboxEgressDSCP: 64},
			wantErr: "SANDBOX_EGRESS_DSCP=64 out of range (0..63)",
		},
		{
			name:    "build value out of range",
			config:  Config{BuildSandboxEgressDSCP: dscp(255)},
			wantErr: "BUILD_SANDBOX_EGRESS_DSCP=255 out of range (0..63)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.config.Validate()
			if tt.wantErr == "" {
				require.NoError(t, err)

				return
			}

			require.EqualError(t, err, tt.wantErr)
		})
	}
}

// TestParseConfig_BuildEgressDSCP pins the env semantics the fallback relies on:
// an absent BUILD_SANDBOX_EGRESS_DSCP must leave the pointer nil rather than
// decode as 0, which would silently disable marking for every build.
func TestParseConfig_BuildEgressDSCP(t *testing.T) { //nolint:paralleltest // t.Setenv
	// Inherited by every subtest.
	t.Setenv("SANDBOX_EGRESS_DSCP", "8")

	t.Run("absent leaves the build value unset", func(t *testing.T) { //nolint:paralleltest // t.Setenv
		config, err := ParseConfig()
		require.NoError(t, err)
		require.Nil(t, config.BuildSandboxEgressDSCP)
		require.Equal(t, uint8(8), config.EgressDSCP(EgressClassBuild))
	})

	t.Run("explicit zero is distinct from absent", func(t *testing.T) { //nolint:paralleltest // t.Setenv
		t.Setenv("BUILD_SANDBOX_EGRESS_DSCP", "0")

		config, err := ParseConfig()
		require.NoError(t, err)
		require.NotNil(t, config.BuildSandboxEgressDSCP)
		require.Equal(t, uint8(0), config.EgressDSCP(EgressClassBuild))
	})

	t.Run("set value is used for builds", func(t *testing.T) { //nolint:paralleltest // t.Setenv
		t.Setenv("BUILD_SANDBOX_EGRESS_DSCP", "16")

		config, err := ParseConfig()
		require.NoError(t, err)
		require.Equal(t, uint8(8), config.EgressDSCP(EgressClassSandbox))
		require.Equal(t, uint8(16), config.EgressDSCP(EgressClassBuild))
	})

	t.Run("out of range fails loudly", func(t *testing.T) { //nolint:paralleltest // t.Setenv
		t.Setenv("BUILD_SANDBOX_EGRESS_DSCP", "64")

		_, err := ParseConfig()
		require.ErrorContains(t, err, "BUILD_SANDBOX_EGRESS_DSCP=64 out of range (0..63)")
	})
}

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

// TestApplyEgressDSCP_RestampsSlotRule verifies the per-tenant re-stamp on a
// real slot: the pool hands out slots that were created before their tenant was
// known, so a build sandbox has to swap the mangle rule installed by
// CreateNetwork and the pool has to swap it back before the slot is reused.
//
// Like TestCreateNetwork_TagsEgressWithDSCP this drives the real netns/iptables
// path and skips without root.
func TestApplyEgressDSCP_RestampsSlotRule(t *testing.T) { //nolint:paralleltest // mutates the caller's netns via LockOSThread + netns.Set; cannot run in parallel
	if os.Geteuid() != 0 {
		t.Skip("requires root for netns + iptables")
	}

	config, err := ParseConfig()
	require.NoError(t, err)
	config.SandboxEgressDSCP = 8             // regular sandboxes: CS1
	config.BuildSandboxEgressDSCP = dscp(16) // template builds: CS2

	const idx = 30001 // high, fixed: avoid collision with the pool's low-index Populate
	slot, err := NewSlot("dscp-restamp-test", idx, config, NewNoopEgressProxy())
	require.NoError(t, err)

	require.NoError(t, slot.CreateNetwork(t.Context()))
	t.Cleanup(func() { _ = slot.RemoveNetwork() })

	requireSingleDSCPRule(t, slot, "0x08") // CreateNetwork seeds the sandbox class

	// A build sandbox takes the slot.
	require.NoError(t, slot.ApplyEgressDSCP(t.Context(), config.EgressDSCP(EgressClassBuild)))
	requireSingleDSCPRule(t, slot, "0x10")

	// Re-applying the same class must not duplicate the rule.
	require.NoError(t, slot.ApplyEgressDSCP(t.Context(), config.EgressDSCP(EgressClassBuild)))
	requireSingleDSCPRule(t, slot, "0x10")

	// The pool recycles the slot back to the sandbox class.
	require.NoError(t, slot.ApplyEgressDSCP(t.Context(), config.EgressDSCP(EgressClassSandbox)))
	requireSingleDSCPRule(t, slot, "0x08")

	// 0 removes the rule outright.
	require.NoError(t, slot.ApplyEgressDSCP(t.Context(), 0))
	require.Emptyf(t, dscpMangleRules(t, slot.NamespaceID()), "DSCP 0 must leave no mangle rule in %s", slot.NamespaceID())
}

// requireSingleDSCPRule asserts the slot's netns holds exactly one DSCP mangle
// rule, on the vpeer uplink, setting the given class.
func requireSingleDSCPRule(t *testing.T, slot *Slot, wantDSCP string) {
	t.Helper()

	rules := dscpMangleRules(t, slot.NamespaceID())

	require.Lenf(t, rules, 1, "want exactly one DSCP mangle rule in %s, got %v", slot.NamespaceID(), rules)
	require.Containsf(t, rules[0], "-o "+slot.VpeerName(), "DSCP rule must match the vpeer uplink: %s", rules[0])
	require.Containsf(t, rules[0], "--set-dscp "+wantDSCP, "DSCP rule must set %s: %s", wantDSCP, rules[0])
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
