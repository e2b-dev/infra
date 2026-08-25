package nodediscovery

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

// Pins the DNS adapter's divergence from the other cached adapters: a refresh
// whose every DNS exchange fails reconciles against the empty result and
// clears the set, instead of preserving the last known entries (see the
// package doc). The semantics decision is deliberately out of scope here.
func TestDnsServiceDiscovery_FailedSyncClearsTheSet(t *testing.T) {
	t.Parallel()

	sd := NewDnsServiceDiscovery(logger.NewNopLogger(), []string{"example.invalid."}, "127.0.0.1:1", cachedPort)
	sd.entries.Insert("10.0.0.1:5008", Instance{ID: "10.0.0.1:5008", IPAddress: "10.0.0.1", Port: cachedPort})

	sd.sync(t.Context())

	instances, err := sd.ListInstances(t.Context())
	require.NoError(t, err)
	assert.Empty(t, instances)
}
