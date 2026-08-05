package nodemanager

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	orchestratorinfo "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator-info"
)

func TestNodeCanAcceptNewRequests(t *testing.T) {
	t.Parallel()

	cases := map[api.NodeStatus]bool{
		api.NodeStatusReady:        true,
		api.NodeStatusConnecting:   false,
		api.NodeStatusDraining:     false,
		api.NodeStatusStandby:      false,
		api.NodeStatusUnhealthy:    false,
		api.NodeStatus("nonsense"): false,
	}

	for status, expected := range cases {
		t.Run(string(status), func(t *testing.T) {
			t.Parallel()

			node := NewTestNode("test-node", status, 0, 1)

			assert.Equal(t, expected, node.CanAcceptNewRequests())
		})
	}
}

// Every status the orchestrator can report needs an api.NodeStatus counterpart.
// Node construction and sync fall back to api.NodeStatusUnhealthy for statuses
// they don't recognize, so a value added to the proto enum without a mapping
// here would silently make every node reporting it look broken — which is how
// standby nodes were once misread as unhealthy. Driving the test off
// ServiceInfoStatus_name means a new proto value fails this test rather than
// slipping through.
func TestOrchestratorToApiNodeStateMapperCoversEveryProtoStatus(t *testing.T) {
	t.Parallel()

	require.NotEmpty(t, orchestratorinfo.ServiceInfoStatus_name)

	for value, name := range orchestratorinfo.ServiceInfoStatus_name {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			orchStatus := orchestratorinfo.ServiceInfoStatus(value)

			apiStatus, ok := OrchestratorToApiNodeStateMapper[orchStatus]
			require.Truef(t, ok, "no api.NodeStatus mapped for orchestrator status %s", name)
			assert.Truef(t, apiStatus.Valid(), "orchestrator status %s maps to %q, which is not a member of the api.NodeStatus enum", name, apiStatus)
		})
	}
}

// The two mappers are used in opposite directions on the same wire values:
// OrchestratorToApiNodeStateMapper translates what a node reports, and
// ApiNodeToOrchestratorStateMapper translates a status override back before
// sending it. They therefore have to be exact inverses, or an override would
// land the node in a status other than the one that was asked for.
func TestStatusMappersAreInverses(t *testing.T) {
	t.Parallel()

	t.Run("orchestrator status round-trips through its api status", func(t *testing.T) {
		t.Parallel()

		for orchStatus, apiStatus := range OrchestratorToApiNodeStateMapper {
			back, ok := ApiNodeToOrchestratorStateMapper[apiStatus]
			require.Truef(t, ok, "%s maps to %q, which maps back to nothing", orchStatus, apiStatus)
			assert.Equalf(t, orchStatus, back, "%s does not round-trip: it maps to %q, which maps back to %s", orchStatus, apiStatus, back)
		}
	})

	t.Run("api status round-trips through its orchestrator status", func(t *testing.T) {
		t.Parallel()

		for apiStatus, orchStatus := range ApiNodeToOrchestratorStateMapper {
			back, ok := OrchestratorToApiNodeStateMapper[orchStatus]
			require.Truef(t, ok, "%q maps to %s, which maps back to nothing", apiStatus, orchStatus)
			assert.Equalf(t, apiStatus, back, "%q does not round-trip: it maps to %s, which maps back to %q", apiStatus, orchStatus, back)
		}
	})

	// api.NodeStatusConnecting is the one asymmetry, and it is deliberate:
	// StatusInfo derives it from the local gRPC channel state, no orchestrator
	// ever reports it, and SendStatusChange must reject an attempt to push it
	// rather than translate it into some neighbouring status.
	t.Run("connecting has no orchestrator counterpart", func(t *testing.T) {
		t.Parallel()

		_, ok := ApiNodeToOrchestratorStateMapper[api.NodeStatusConnecting]
		assert.False(t, ok, "api.NodeStatusConnecting is derived locally and must not be sent to an orchestrator")
	})
}
