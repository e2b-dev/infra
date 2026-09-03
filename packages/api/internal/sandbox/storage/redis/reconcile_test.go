package redis

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/api/internal/sandbox/sandboxtypes"
)

// TestReconcile_SpansSeveralBatches pins that Reconcile classifies every node
// sandbox correctly when one team's candidates are split across several MGET
// commands: each result has to line up with the candidate it was fetched for,
// or a live sandbox in one chunk would be reported as an orphan from another.
func TestReconcile_SpansSeveralBatches(t *testing.T) {
	t.Parallel()

	storage, client := setupTestStorage(t)
	teamID := uuid.New()
	started := time.Now().Add(-2 * orphanGracePeriod)

	// More than two full chunks plus a partial one, all in the store.
	stored := sandboxScanBatchSize*2 + 37
	seedTeamSandboxes(t, client, teamID, stored, sandboxtypes.StateRunning)

	var nodeSandboxes []sandboxtypes.NodeSandbox
	for i := range stored {
		nodeSandboxes = append(nodeSandboxes, sandboxtypes.NodeSandbox{
			SandboxID: fmt.Sprintf("sbx-%s-%04d", sandboxtypes.StateRunning, i),
			TeamID:    teamID,
			StartTime: started,
		})
	}

	// Orphans the node reports but the store never saw, placed so that they
	// land in the first, a middle, and the final chunk.
	orphanIDs := []string{"orphan-first", "orphan-middle", "orphan-last"}
	orphans := []sandboxtypes.NodeSandbox{
		{SandboxID: orphanIDs[0], TeamID: teamID, StartTime: started},
		{SandboxID: orphanIDs[1], TeamID: teamID, StartTime: started},
		{SandboxID: orphanIDs[2], TeamID: teamID, StartTime: started},
	}
	nodeSandboxes = slices.Insert(nodeSandboxes, 0, orphans[0])
	nodeSandboxes = slices.Insert(nodeSandboxes, sandboxScanBatchSize+10, orphans[1])
	nodeSandboxes = append(nodeSandboxes, orphans[2])

	// Too young to be judged, even though it is not in the store.
	nodeSandboxes = append(nodeSandboxes, sandboxtypes.NodeSandbox{
		SandboxID: "fresh-not-yet-stored",
		TeamID:    teamID,
		StartTime: time.Now(),
	})

	got := storage.Reconcile(t.Context(), nodeSandboxes, "test-node")

	gotIDs := make([]string, 0, len(got))
	for _, sbx := range got {
		gotIDs = append(gotIDs, sbx.SandboxID)
	}
	require.ElementsMatch(t, orphanIDs, gotIDs, "exactly the stored-nowhere, old-enough sandboxes are orphans")
	for _, sbx := range got {
		assert.Equal(t, teamID, sbx.TeamID)
	}
}
