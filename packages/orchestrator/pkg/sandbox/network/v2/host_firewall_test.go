//go:build linux

package v2

import (
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/network"
)

func testConfig() network.Config {
	return network.Config{
		OrchestratorInSandboxIPAddress: "192.0.2.1",
		HyperloopProxyPort:             5010,
		NFSProxyPort:                   5011,
		PortmapperPort:                 5012,
		SandboxTCPFirewallHTTPPort:     5016,
		SandboxTCPFirewallTLSPort:      5017,
		SandboxTCPFirewallOtherPort:    5018,
	}
}

func TestHostFirewall_Init(t *testing.T) { //nolint:paralleltest // creates/deletes the singleton nftables table "v2-host-firewall" shared by all HostFirewall tests
	skipIfNotLinuxRoot(t)

	hf, err := NewHostFirewall("lo", testConfig())
	require.NoError(t, err)
	defer hf.Close()

	assert.NotNil(t, hf.conn)
	assert.NotNil(t, hf.table)
	assert.NotNil(t, hf.vethSet)
	assert.NotNil(t, hf.cidrSet)
}

func TestHostFirewall_AddRemoveSlot(t *testing.T) { //nolint:paralleltest // creates/deletes the singleton nftables table "v2-host-firewall" shared by all HostFirewall tests
	skipIfNotLinuxRoot(t)

	hf, err := NewHostFirewall("lo", testConfig())
	require.NoError(t, err)
	defer hf.Close()

	slot := makeTestSlot(t, 1)
	sv2 := NewSlotV2(slot)

	// Add slot
	err = hf.AddSlot(sv2)
	require.NoError(t, err)

	// Verify veth set has the element
	elements, err := hf.conn.GetSetElements(hf.vethSet)
	require.NoError(t, err)
	assert.NotEmpty(t, elements, "veth set should have elements")

	// Verify cidr set has elements
	cidrElements, err := hf.conn.GetSetElements(hf.cidrSet)
	require.NoError(t, err)
	assert.NotEmpty(t, cidrElements, "cidr set should have elements")

	// Remove slot
	err = hf.RemoveSlot(sv2)
	require.NoError(t, err)

	// Verify veth set is empty
	elements, err = hf.conn.GetSetElements(hf.vethSet)
	require.NoError(t, err)
	assert.Empty(t, elements, "veth set should be empty after removal")
}

func TestHostFirewall_ConcurrentAccess(t *testing.T) { //nolint:paralleltest // creates/deletes the singleton nftables table "v2-host-firewall" shared by all HostFirewall tests
	skipIfNotLinuxRoot(t)

	hf, err := NewHostFirewall("lo", testConfig())
	require.NoError(t, err)
	defer hf.Close()

	// Add multiple slots concurrently
	done := make(chan error, 10)
	for i := 1; i <= 10; i++ {
		sv2 := NewSlotV2(makeTestSlot(t, i))
		go func() {
			done <- hf.AddSlot(sv2)
		}()
	}

	for range 10 {
		err := <-done
		require.NoError(t, err, "concurrent AddSlot should not fail")
	}

	// Verify all 10 veths added
	elements, err := hf.conn.GetSetElements(hf.vethSet)
	require.NoError(t, err)
	assert.Len(t, elements, 10)
}

func TestIncrementIP(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input    string
		expected string
	}{
		{"10.11.0.1", "10.11.0.2"},
		{"10.11.0.255", "10.11.1.0"},
		{"10.11.255.255", "10.12.0.0"},
		{"192.168.1.1", "192.168.1.2"},
	}

	for _, tt := range tests {
		ip := net.ParseIP(tt.input).To4()
		result := incrementIP(ip)
		assert.Equal(t, tt.expected, result.String(), "incrementIP(%s)", tt.input)
	}
}

func TestPortBytes(t *testing.T) {
	t.Parallel()

	b := portBytes(80)
	assert.Equal(t, []byte{0, 80}, b)

	b = portBytes(443)
	assert.Equal(t, []byte{1, 187}, b)

	b = portBytes(5010)
	assert.Equal(t, []byte{0x13, 0x92}, b)
}
