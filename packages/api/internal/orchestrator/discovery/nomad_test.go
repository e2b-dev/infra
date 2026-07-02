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

const testServiceName = "orchestrator"

// newNomadServiceMock returns a nomad API client backed by an httptest server
// that serves the given registrations from GET /v1/service/<testServiceName>,
// mirroring the agent endpoint's behavior (200 + JSON array, empty array when
// there are no registrations).
func newNomadServiceMock(t *testing.T, regs []*nomadapi.ServiceRegistration) *nomadapi.Client {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/service/"+testServiceName {
			if regs == nil {
				regs = []*nomadapi.ServiceRegistration{}
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(regs)

			return
		}

		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)

	client, err := nomadapi.NewClient(&nomadapi.Config{Address: srv.URL})
	require.NoError(t, err)

	return client
}

// TestNomadDiscovery_MapsRegistrations verifies field mapping: ShortID is the
// truncated Nomad node ID (stable across the node-listing -> service-listing
// backend switch) and OrchestratorAddress uses the registration's bound port.
func TestNomadDiscovery_MapsRegistrations(t *testing.T) {
	t.Parallel()

	fullNodeID := "aabbccdd11223344aabbccdd11223344aabbccdd"
	client := newNomadServiceMock(t, []*nomadapi.ServiceRegistration{
		{
			ID:          "_nomad-task-a-orchestrator",
			ServiceName: testServiceName,
			NodeID:      fullNodeID,
			Address:     "10.0.0.1",
			Port:        5008,
		},
	})

	d := NewNomad(client, testServiceName)
	nodes, err := d.ListNodes(t.Context())
	require.NoError(t, err)
	require.Len(t, nodes, 1)

	assert.Equal(t, fullNodeID[:consts.NodeIDLength], nodes[0].ShortID)
	assert.Equal(t, "10.0.0.1", nodes[0].IPAddress)
	assert.Equal(t, net.JoinHostPort("10.0.0.1", "5008"), nodes[0].OrchestratorAddress)
}

// TestNomadDiscovery_DeduplicatesByNode ensures that two registrations on the
// same node (a transient state, e.g. a stopping allocation whose registration
// has not been reaped yet) collapse into a single discovered node, keeping
// ShortIDs unique for callers that key on them.
func TestNomadDiscovery_DeduplicatesByNode(t *testing.T) {
	t.Parallel()

	nodeID := "aabbccdd11223344aabbccdd11223344aabbccdd"
	client := newNomadServiceMock(t, []*nomadapi.ServiceRegistration{
		{ID: "_nomad-task-old-orchestrator", ServiceName: testServiceName, NodeID: nodeID, Address: "10.0.0.1", Port: 5008},
		{ID: "_nomad-task-new-orchestrator", ServiceName: testServiceName, NodeID: nodeID, Address: "10.0.0.1", Port: 5008},
		{ID: "_nomad-task-other-orchestrator", ServiceName: testServiceName, NodeID: "eeff00112233445566778899aabbccddeeff0011", Address: "10.0.0.2", Port: 5008},
	})

	d := NewNomad(client, testServiceName)
	nodes, err := d.ListNodes(t.Context())
	require.NoError(t, err)
	require.Len(t, nodes, 2)
	assert.NotEqual(t, nodes[0].ShortID, nodes[1].ShortID)
}

// TestNomadDiscovery_PortFallback ensures a registration without a bound port
// falls back to the well-known orchestrator port.
func TestNomadDiscovery_PortFallback(t *testing.T) {
	t.Parallel()

	client := newNomadServiceMock(t, []*nomadapi.ServiceRegistration{
		{ID: "_nomad-task-a-orchestrator", ServiceName: testServiceName, NodeID: "aabbccdd11223344aabbccdd11223344aabbccdd", Address: "10.0.0.1"},
	})

	d := NewNomad(client, testServiceName)
	nodes, err := d.ListNodes(t.Context())
	require.NoError(t, err)
	require.Len(t, nodes, 1)

	port := strconv.Itoa(int(consts.OrchestratorAPIPort))
	assert.Equal(t, net.JoinHostPort("10.0.0.1", port), nodes[0].OrchestratorAddress)
}

// TestNomadDiscovery_SkipsMissingAddress ensures registrations without an
// address are excluded so callers never dial an empty host.
func TestNomadDiscovery_SkipsMissingAddress(t *testing.T) {
	t.Parallel()

	client := newNomadServiceMock(t, []*nomadapi.ServiceRegistration{
		{ID: "_nomad-task-a-orchestrator", ServiceName: testServiceName, NodeID: "aabbccdd11223344aabbccdd11223344aabbccdd", Port: 5008},
	})

	d := NewNomad(client, testServiceName)
	nodes, err := d.ListNodes(t.Context())
	require.NoError(t, err)
	assert.Empty(t, nodes)
}

// TestNomadDiscovery_EmptyService ensures an empty registration list (the
// agent returns 200 + [] when no instances are registered) yields an empty,
// non-error result.
func TestNomadDiscovery_EmptyService(t *testing.T) {
	t.Parallel()

	client := newNomadServiceMock(t, nil)

	d := NewNomad(client, testServiceName)
	nodes, err := d.ListNodes(t.Context())
	require.NoError(t, err)
	assert.Empty(t, nodes)
}
