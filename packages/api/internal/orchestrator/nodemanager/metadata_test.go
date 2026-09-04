package nodemanager

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	"github.com/e2b-dev/infra/packages/shared/pkg/edge"
)

// The delete event the API attaches for a cluster node carries the restore
// decision the edge acts on; a local node attaches no event at all.
func TestGetSandboxDeleteCtx_CarriesTheRestoreDecisionToTheEdge(t *testing.T) {
	t.Parallel()

	for name, restore := range map[string]bool{"restore on": true, "restore off": false} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			node := NewTestNode("node-1", api.NodeStatusReady, 0, 8)
			node.ClusterID = uuid.New()

			_, ctx := node.GetSandboxDeleteCtx(t.Context(), "sbx-1", "exec-1", restore)
			md, ok := metadata.FromOutgoingContext(ctx)
			require.True(t, ok)

			ev, err := edge.ParseSandboxCatalogDeleteEvent(md)
			require.NoError(t, err)
			assert.Equal(t, "sbx-1", ev.SandboxID)
			assert.Equal(t, "exec-1", ev.ExecutionID)
			assert.Equal(t, restore, ev.RestoreOnRefusal)
		})
	}

	t.Run("local node attaches no event", func(t *testing.T) {
		t.Parallel()

		node := NewTestNode("node-1", api.NodeStatusReady, 0, 8)
		node.ClusterID = consts.LocalClusterID

		_, ctx := node.GetSandboxDeleteCtx(t.Context(), "sbx-1", "exec-1", true)
		md, _ := metadata.FromOutgoingContext(ctx)
		assert.Empty(t, md.Get(edge.EventTypeHeader))
	})
}
