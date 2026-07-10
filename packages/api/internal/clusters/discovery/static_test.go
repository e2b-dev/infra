package discovery

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewStaticAddressDiscovery(t *testing.T) {
	t.Parallel()

	discovery, err := NewStaticAddressDiscovery("127.0.0.1:5008")
	require.NoError(t, err)

	items, err := discovery.Query(context.Background())
	require.NoError(t, err)
	require.Equal(t, []Item{{
		UniqueIdentifier:     "local",
		NodeID:               "local",
		InstanceID:           "unknown",
		LocalIPAddress:       "127.0.0.1",
		LocalInstanceApiPort: 5008,
	}}, items)
}

func TestNewStaticAddressDiscoveryRejectsInvalidAddress(t *testing.T) {
	t.Parallel()

	for _, address := range []string{"not-an-address", ":5008", "127.0.0.1:0"} {
		_, err := NewStaticAddressDiscovery(address)
		require.Error(t, err, address)
	}
}
