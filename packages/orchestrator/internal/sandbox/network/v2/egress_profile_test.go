package v2

import (
	"net"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEgressManager_RegisterAndGet(t *testing.T) {
	mgr := NewEgressManager()

	profile := &EgressProfile{
		ID:          "test-profile",
		OwnerType:   "customer",
		OwnerID:     "cust-123",
		Mode:        "customer-shared",
		BackendType: "gateway",
		FwMark:      0x300,
		RouteTableID: 300,
		WgDevice:    "wg0",
		GatewayAddr: net.ParseIP("10.99.0.1"),
		GatewaySNATIP: net.ParseIP("192.168.100.200"),
		PublicIPSet: []net.IP{net.ParseIP("192.168.100.200")},
	}

	err := mgr.Register(profile)
	assert.NoError(t, err)

	// Duplicate registration should fail
	err = mgr.Register(profile)
	assert.Error(t, err)

	// Get
	got := mgr.GetProfile("test-profile")
	assert.NotNil(t, got)
	assert.Equal(t, "test-profile", got.ID)
	assert.Equal(t, uint32(0x300), got.FwMark)

	// Get non-existent
	assert.Nil(t, mgr.GetProfile("nonexistent"))
}

func TestEgressProfile_DirectBackend(t *testing.T) {
	mgr := NewEgressManager()

	profile := DefaultEgressProfile()
	require.NoError(t, mgr.Register(profile))

	// Direct backend should not setup routing
	err := mgr.SetupRouting(profile)
	assert.NoError(t, err)

	err = mgr.TeardownRouting(profile)
	assert.NoError(t, err)
}

func TestEgressProfile_RouteSetup(t *testing.T) {
	skipIfNotLinuxRoot(t)

	mgr := NewEgressManager()

	// Create a dummy device with an address so the gateway is reachable
	err := exec.Command("ip", "link", "add", "dummy-egr", "type", "dummy").Run()
	require.NoError(t, err)
	defer exec.Command("ip", "link", "del", "dummy-egr").Run()

	require.NoError(t, exec.Command("ip", "link", "set", "dummy-egr", "up").Run())
	require.NoError(t, exec.Command("ip", "addr", "add", "10.99.0.2/24", "dev", "dummy-egr").Run())

	profile := &EgressProfile{
		ID:           "gw-profile",
		BackendType:  "gateway",
		FwMark:       0x400,
		RouteTableID: 400,
		GatewayAddr:  net.ParseIP("10.99.0.1"),
		WgDevice:     "dummy-egr",
	}

	require.NoError(t, mgr.Register(profile))

	err = mgr.SetupRouting(profile)
	assert.NoError(t, err)
	defer mgr.TeardownRouting(profile)
}

func TestEgressProfile_SharedByMultipleSlots(t *testing.T) {
	skipIfNotLinuxRoot(t)

	mgr := NewEgressManager()
	hf, err := NewHostFirewall("lo", testConfig())
	require.NoError(t, err)
	defer hf.Close()

	profile := &EgressProfile{
		ID:          "shared-profile",
		Mode:        "customer-shared",
		BackendType: "direct",
		FwMark:      0x500,
	}
	require.NoError(t, mgr.Register(profile))

	// Assign multiple slots to the same profile
	for i := 1; i <= 3; i++ {
		slot := makeTestSlot(t, i)
		sv2 := NewSlotV2(slot)

		err := mgr.AssignSlot(hf, sv2, "shared-profile")
		assert.NoError(t, err)
		assert.Equal(t, "shared-profile", sv2.EgressProfileID)
	}
}

func TestEgressManager_Close(t *testing.T) {
	mgr := NewEgressManager()

	p1 := DefaultEgressProfile()
	p1.ID = "p1"
	p2 := DefaultEgressProfile()
	p2.ID = "p2"

	require.NoError(t, mgr.Register(p1))
	require.NoError(t, mgr.Register(p2))

	err := mgr.Close()
	assert.NoError(t, err)

	// After close, profiles should be cleared
	assert.Nil(t, mgr.GetProfile("p1"))
	assert.Nil(t, mgr.GetProfile("p2"))
}
