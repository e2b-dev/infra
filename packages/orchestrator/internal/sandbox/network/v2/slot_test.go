package v2

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/network"
)

func makeTestSlot(t *testing.T, idx int) *network.Slot {
	t.Helper()
	cfg := network.Config{
		OrchestratorInSandboxIPAddress: "192.0.2.1",
		HyperloopProxyPort:             5010,
		NFSProxyPort:                   5011,
		PortmapperPort:                 5012,
		SandboxTCPFirewallHTTPPort:     5016,
		SandboxTCPFirewallTLSPort:      5017,
		SandboxTCPFirewallOtherPort:    5018,
	}
	slot, err := network.NewSlot("test-key", idx, cfg)
	require.NoError(t, err)
	return slot
}

func TestSlotV2_Creation(t *testing.T) {
	slot := makeTestSlot(t, 1)
	sv2 := NewSlotV2(slot)

	assert.Equal(t, 2, sv2.NetworkVersion)
	assert.Equal(t, slot, sv2.Slot)
	assert.Equal(t, uint32(fwMarkBase+1), sv2.FwMark)
	assert.Equal(t, "", sv2.SandboxID)
	assert.Equal(t, "", sv2.EgressProfileID)
}

func TestSlotV2_FwMarkUniqueness(t *testing.T) {
	marks := make(map[uint32]bool)

	for i := 1; i <= 100; i++ {
		slot := makeTestSlot(t, i)
		sv2 := NewSlotV2(slot)

		assert.False(t, marks[sv2.FwMark], "duplicate fwmark at index %d: 0x%x", i, sv2.FwMark)
		marks[sv2.FwMark] = true
	}
}

func TestSlotV2_FwMarkForIndex(t *testing.T) {
	assert.Equal(t, uint32(0x201), FwMarkForIndex(1))
	assert.Equal(t, uint32(0x264), FwMarkForIndex(100))
}

func TestSlotV2_String(t *testing.T) {
	slot := makeTestSlot(t, 5)
	sv2 := NewSlotV2(slot)
	sv2.EgressProfileID = "test-profile"

	s := sv2.String()
	assert.Contains(t, s, "idx=5")
	assert.Contains(t, s, "fwmark=0x205")
	assert.Contains(t, s, "egress=test-profile")
}

func TestSlotV2Registry(t *testing.T) {
	reg := NewSlotV2Registry()

	slot1 := makeTestSlot(t, 1)
	sv2_1 := NewSlotV2(slot1)
	sv2_1.SandboxID = "sb-1"

	slot2 := makeTestSlot(t, 2)
	sv2_2 := NewSlotV2(slot2)
	sv2_2.SandboxID = "sb-2"

	// Store
	reg.Store(sv2_1)
	reg.Store(sv2_2)

	// Load
	loaded, ok := reg.Load(1)
	assert.True(t, ok)
	assert.Equal(t, "sb-1", loaded.SandboxID)

	_, ok = reg.Load(99)
	assert.False(t, ok)

	// Delete
	reg.Delete(1)
	_, ok = reg.Load(1)
	assert.False(t, ok)

	// Range
	count := 0
	reg.Range(func(idx int, s *SlotV2) bool {
		count++
		return true
	})
	assert.Equal(t, 1, count)
}

func TestSlotV2_EmbeddedSlotMethods(t *testing.T) {
	slot := makeTestSlot(t, 3)
	sv2 := NewSlotV2(slot)

	// Access base slot methods through composition
	assert.Equal(t, "veth-3", sv2.Slot.VethName())
	assert.Equal(t, "ns-3", sv2.Slot.NamespaceID())
	assert.NotNil(t, sv2.Slot.HostIP)
}
