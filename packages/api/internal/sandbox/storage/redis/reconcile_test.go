package redis

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/api/internal/sandbox/sandboxtypes"
)

// mgetRecorder is a go-redis hook that records the key count of every MGET the
// client sends, including MGETs inside pipelines.
type mgetRecorder struct {
	mu        sync.Mutex
	keyCounts []int
}

func (r *mgetRecorder) record(cmd redis.Cmder) {
	if cmd.Name() != "mget" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	// args = ["mget", key1, key2, ...]
	r.keyCounts = append(r.keyCounts, len(cmd.Args())-1)
}

func (r *mgetRecorder) reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.keyCounts = nil
}

func (r *mgetRecorder) snapshot() []int {
	r.mu.Lock()
	defer r.mu.Unlock()

	return append([]int(nil), r.keyCounts...)
}

func (r *mgetRecorder) DialHook(next redis.DialHook) redis.DialHook {
	return next
}

func (r *mgetRecorder) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		r.record(cmd)

		return next(ctx, cmd)
	}
}

func (r *mgetRecorder) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		for _, cmd := range cmds {
			r.record(cmd)
		}

		return next(ctx, cmds)
	}
}

// TestReconcile_SpansSeveralBatches pins that Reconcile classifies every node
// sandbox correctly when one team's candidates are split across several MGET
// commands: each result has to line up with the candidate it was fetched for,
// or a live sandbox in one chunk would be reported as an orphan from another.
//
// It also asserts the chunking itself through a client hook: the number of
// MGETs sent, that none carries more than sandboxScanBatchSize keys, and that
// the key counts add up to the candidate count. The orphan-set check alone
// passes with a single unbounded MGET, so without this a revert of the
// chunking would go unnoticed.
func TestReconcile_SpansSeveralBatches(t *testing.T) {
	t.Parallel()

	storage, client := setupTestStorage(t)
	teamID := uuid.New()
	started := time.Now().Add(-2 * orphanGracePeriod)

	rec := &mgetRecorder{}
	client.AddHook(rec)

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

	// Seeding does not MGET, but reset so the assertions below see only what
	// Reconcile sent.
	rec.reset()

	got := storage.Reconcile(t.Context(), nodeSandboxes, "test-node")

	gotIDs := make([]string, 0, len(got))
	for _, sbx := range got {
		gotIDs = append(gotIDs, sbx.SandboxID)
	}
	require.ElementsMatch(t, orphanIDs, gotIDs, "exactly the stored-nowhere, old-enough sandboxes are orphans")
	for _, sbx := range got {
		assert.Equal(t, teamID, sbx.TeamID)
	}

	// Candidates = stored + 3 orphans; the fresh one is filtered before any
	// read. 552 keys / 256 per command = 3 commands: 256, 256, 40.
	candidates := stored + len(orphans)
	counts := rec.snapshot()
	wantCmds := (candidates + sandboxScanBatchSize - 1) / sandboxScanBatchSize
	require.Len(t, counts, wantCmds, "MGET commands sent: %v", counts)
	sum := 0
	for _, n := range counts {
		require.LessOrEqual(t, n, sandboxScanBatchSize, "an MGET exceeded the per-command key cap: %v", counts)
		sum += n
	}
	require.Equal(t, candidates, sum, "MGET key counts should cover every candidate exactly once: %v", counts)
}
