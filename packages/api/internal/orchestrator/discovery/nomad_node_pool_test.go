package discovery

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	nomadapi "github.com/hashicorp/nomad/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
)

// newNomadNodesMock returns a nomad API client backed by an httptest server
// that serves the given node stubs from GET /v1/nodes, mirroring the agent
// endpoint's behavior. The filter expression the client sends is captured
// into gotFilter (server-side filtering itself is not simulated).
func newNomadNodesMock(t *testing.T, stubs []*nomadapi.NodeListStub, gotFilter *string) *nomadapi.Client {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/nodes" {
			if gotFilter != nil {
				*gotFilter = r.URL.Query().Get("filter")
			}
			if stubs == nil {
				stubs = []*nomadapi.NodeListStub{}
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(stubs)

			return
		}

		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)

	client, err := nomadapi.NewClient(&nomadapi.Config{Address: srv.URL})
	require.NoError(t, err)

	return client
}

// TestNomadNodePoolDiscovery_MapsNodes verifies field mapping: ShortID is the
// truncated Nomad node ID (identical to what the service-based backend
// produces for the same node, so the merged union cannot create duplicate
// identities) and OrchestratorAddress uses the well-known orchestrator port.
func TestNomadNodePoolDiscovery_MapsNodes(t *testing.T) {
	t.Parallel()

	fullNodeID := "aabbccdd11223344aabbccdd11223344aabbccdd"
	client := newNomadNodesMock(t, []*nomadapi.NodeListStub{
		{ID: fullNodeID, Address: "10.0.0.1", Status: "ready", NodePool: "default"},
	}, nil)

	d := NewNomadNodePool(client, "default")
	nodes, err := d.ListNodes(t.Context())
	require.NoError(t, err)
	require.Len(t, nodes, 1)

	port := strconv.Itoa(int(consts.OrchestratorAPIPort))
	assert.Equal(t, fullNodeID[:consts.NodeIDLength], nodes[0].ShortID)
	assert.Equal(t, "10.0.0.1", nodes[0].IPAddress)
	assert.Equal(t, net.JoinHostPort("10.0.0.1", port), nodes[0].OrchestratorAddress)
}

// TestNomadNodePoolDiscovery_FiltersByStatusAndPool verifies the server-side
// filter expression restricts the listing to ready nodes in the configured
// pool, matching the pre-service-discovery implementation.
func TestNomadNodePoolDiscovery_FiltersByStatusAndPool(t *testing.T) {
	t.Parallel()

	var gotFilter string
	client := newNomadNodesMock(t, nil, &gotFilter)

	d := NewNomadNodePool(client, "orchestrator-pool")
	nodes, err := d.ListNodes(t.Context())
	require.NoError(t, err)
	assert.Empty(t, nodes)
	assert.Equal(t, `Status == "ready" and NodePool == "orchestrator-pool"`, gotFilter)
}
