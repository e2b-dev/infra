package servicediscovery

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
)

func TestNewLocal_ResolvesTheAddress(t *testing.T) {
	t.Parallel()

	defaultPort := strconv.Itoa(int(consts.OrchestratorAPIPort))

	tests := map[string]struct{ addr, wantAddress string }{
		"host and port":     {addr: "10.0.0.1:6123", wantAddress: "10.0.0.1:6123"},
		"host only":         {addr: "10.0.0.1", wantAddress: "10.0.0.1:" + defaultPort},
		"hostname and port": {addr: "orchestrator:6123", wantAddress: "orchestrator:6123"},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			d, err := NewLocal(tt.addr)
			require.NoError(t, err)

			instances, err := d.ListInstances(t.Context())
			require.NoError(t, err)
			require.Len(t, instances, 1)

			assert.Equal(t, "local", instances[0].WorkloadID)
			assert.Equal(t, "local", instances[0].NodeID)
			assert.Equal(t, tt.wantAddress, instances[0].Address())
			assert.Equal(t, BackendLocal, instances[0].Backend)
		})
	}
}

func TestNewStatic_NamesItsBackend(t *testing.T) {
	t.Parallel()

	items, err := NewStatic([]string{"10.0.0.1"}, 5008).ListInstances(t.Context())
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, BackendStatic, items[0].Backend)
}

// Every lister names itself, so a caller grouping by backend never has to
// account for an empty bucket.
func TestBackend_EveryListerNamesItself(t *testing.T) {
	t.Parallel()

	assert.Equal(t, []string{"nomad", "kubernetes", "local", "remote", "dns", "static"},
		[]string{BackendNomad, BackendKubernetes, BackendLocal, BackendRemote, BackendDNS, BackendStatic})
}

func TestNewLocal_RejectsAnUnusableAddress(t *testing.T) {
	t.Parallel()

	for name, addr := range map[string]string{
		"empty":             "",
		"no host":           ":6123",
		"port too big":      "10.0.0.1:70000",
		"port not a number": "10.0.0.1:grpc",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := NewLocal(addr)
			require.Error(t, err)
		})
	}
}
