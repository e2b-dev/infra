package discovery

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	nomadapi "github.com/hashicorp/nomad/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
)

const testServiceName = "orchestrator"

// newNomadServicesMock returns a nomad API client backed by an httptest
// server that serves the given registrations per service name from
// GET /v1/service/<name>, mirroring the agent endpoint's behavior
// (200 + JSON array, empty array when there are no registrations). Unlisted
// service names return 200 + [] as well, matching the real agent.
func newNomadServicesMock(t *testing.T, regsByService map[string][]*nomadapi.ServiceRegistration) *nomadapi.Client {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name, ok := strings.CutPrefix(r.URL.Path, "/v1/service/")
		if !ok {
			http.NotFound(w, r)

			return
		}

		regs := regsByService[name]
		if regs == nil {
			regs = []*nomadapi.ServiceRegistration{}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(regs)
	}))
	t.Cleanup(srv.Close)

	client, err := nomadapi.NewClient(&nomadapi.Config{Address: srv.URL})
	require.NoError(t, err)

	return client
}

// newNomadServiceMock is a single-service convenience wrapper around
// newNomadServicesMock.
func newNomadServiceMock(t *testing.T, regs []*nomadapi.ServiceRegistration) *nomadapi.Client {
	t.Helper()

	return newNomadServicesMock(t, map[string][]*nomadapi.ServiceRegistration{testServiceName: regs})
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

	d := NewNomad(client, []string{testServiceName})
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

	d := NewNomad(client, []string{testServiceName})
	nodes, err := d.ListNodes(t.Context())
	require.NoError(t, err)
	require.Len(t, nodes, 2)
	assert.NotEqual(t, nodes[0].ShortID, nodes[1].ShortID)
}

// TestNomadDiscovery_UnionsServices ensures registrations from multiple
// configured service names are unioned, with nodes registered under several
// names collapsed to a single entry (earlier service names win).
func TestNomadDiscovery_UnionsServices(t *testing.T) {
	t.Parallel()

	sharedNodeID := "aabbccdd11223344aabbccdd11223344aabbccdd"
	client := newNomadServicesMock(t, map[string][]*nomadapi.ServiceRegistration{
		testServiceName: {
			{ID: "_nomad-task-a-orchestrator", ServiceName: testServiceName, NodeID: sharedNodeID, Address: "10.0.0.1", Port: 5008},
		},
		"orchestrator-canary": {
			// Same node also registered under the second service name: must
			// collapse, with the first service's entry winning.
			{ID: "_nomad-task-a-canary", ServiceName: "orchestrator-canary", NodeID: sharedNodeID, Address: "10.0.0.1", Port: 6008},
			{ID: "_nomad-task-b-canary", ServiceName: "orchestrator-canary", NodeID: "eeff00112233445566778899aabbccddeeff0011", Address: "10.0.0.2", Port: 5008},
		},
	})

	d := NewNomad(client, []string{testServiceName, "orchestrator-canary"})
	nodes, err := d.ListNodes(t.Context())
	require.NoError(t, err)
	require.Len(t, nodes, 2)

	assert.Equal(t, net.JoinHostPort("10.0.0.1", "5008"), nodes[0].OrchestratorAddress)
	assert.Equal(t, net.JoinHostPort("10.0.0.2", "5008"), nodes[1].OrchestratorAddress)
}

// TestNomadDiscovery_SkipsMissingAddress ensures registrations without an
// address are excluded so callers never dial an empty host.
func TestNomadDiscovery_SkipsMissingAddress(t *testing.T) {
	t.Parallel()

	client := newNomadServiceMock(t, []*nomadapi.ServiceRegistration{
		{ID: "_nomad-task-a-orchestrator", ServiceName: testServiceName, NodeID: "aabbccdd11223344aabbccdd11223344aabbccdd", Port: 5008},
	})

	d := NewNomad(client, []string{testServiceName})
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

	d := NewNomad(client, []string{testServiceName})
	nodes, err := d.ListNodes(t.Context())
	require.NoError(t, err)
	assert.Empty(t, nodes)
}
