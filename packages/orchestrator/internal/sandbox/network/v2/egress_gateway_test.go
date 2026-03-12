package v2

import (
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEgressGateway_SNATRules(t *testing.T) {
	skipIfNotLinuxRoot(t)

	cfg := EgressGatewayConfig{
		WgInterface:   "lo", // use lo for testing (wg0 won't exist in test env)
		ExternalIface: "lo",
		SNATRules: []SNATRule{
			{
				SourceCIDR: "10.11.0.0/16",
				SNATIP:     net.ParseIP("192.168.100.200"),
			},
		},
	}

	err := SetupEgressGateway(cfg)
	require.NoError(t, err)

	// Cleanup
	err = TeardownEgressGateway()
	assert.NoError(t, err)
}

func TestEgressGateway_MultipleSNATRules(t *testing.T) {
	skipIfNotLinuxRoot(t)

	cfg := EgressGatewayConfig{
		WgInterface:   "lo",
		ExternalIface: "lo",
		SNATRules: []SNATRule{
			{
				SourceCIDR: "10.11.0.0/16",
				SNATIP:     net.ParseIP("192.168.100.200"),
				FwMark:     0x300,
			},
			{
				SourceCIDR: "10.11.0.0/16",
				SNATIP:     net.ParseIP("192.168.100.201"),
				FwMark:     0x301,
			},
		},
	}

	err := SetupEgressGateway(cfg)
	require.NoError(t, err)
	defer TeardownEgressGateway()
}

func TestEgressGateway_InvalidSNATIP(t *testing.T) {
	skipIfNotLinuxRoot(t)

	cfg := EgressGatewayConfig{
		WgInterface:   "lo",
		ExternalIface: "lo",
		SNATRules: []SNATRule{
			{
				SourceCIDR: "10.11.0.0/16",
				SNATIP:     nil, // invalid
			},
		},
	}

	err := SetupEgressGateway(cfg)
	assert.Error(t, err)
	TeardownEgressGateway() // cleanup even on error
}

func TestEgressGateway_InvalidSourceCIDR(t *testing.T) {
	skipIfNotLinuxRoot(t)

	cfg := EgressGatewayConfig{
		WgInterface:   "lo",
		ExternalIface: "lo",
		SNATRules: []SNATRule{
			{
				SourceCIDR: "not-a-cidr",
				SNATIP:     net.ParseIP("192.168.100.200"),
			},
		},
	}

	err := SetupEgressGateway(cfg)
	assert.Error(t, err)
	TeardownEgressGateway()
}
