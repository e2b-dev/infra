package redis

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/api/internal/sandbox/sandboxtypes"
)

func TestAllRunningItems_Empty(t *testing.T) {
	t.Parallel()

	storage, _ := setupTestStorage(t)

	items, err := storage.AllRunningItems(t.Context())
	require.NoError(t, err)
	assert.Empty(t, items)
}

func TestAllRunningItems_MultipleBatchesAndStateFilter(t *testing.T) {
	t.Parallel()

	storage, client := setupTestStorage(t)

	teamA := uuid.New()
	teamB := uuid.New()

	// Force >2 SSCAN/MGET batches with a partial tail for team A.
	totalA := sandboxScanBatchSize*2 + 37
	seedTeamSandboxes(t, client, teamA, totalA, sandboxtypes.StateRunning)

	// Team B: a few running plus non-running records that must be filtered.
	seedTeamSandboxes(t, client, teamB, 5, sandboxtypes.StateRunning)
	seedTeamSandboxes(t, client, teamB, 3, sandboxtypes.StateKilling)

	items, err := storage.AllRunningItems(t.Context())
	require.NoError(t, err)

	assert.Len(t, items, totalA+5, "must return all running sandboxes across teams and skip non-running states")

	perTeam := map[uuid.UUID]int{}
	for _, sbx := range items {
		assert.Equal(t, sandboxtypes.StateRunning, sbx.State)
		perTeam[sbx.TeamID]++
	}
	assert.Equal(t, totalA, perTeam[teamA])
	assert.Equal(t, 5, perTeam[teamB])
}

func TestAllRunningItems_ToleratesStaleIndexEntries(t *testing.T) {
	t.Parallel()

	storage, client := setupTestStorage(t)

	teamID := uuid.New()
	seedTeamSandboxes(t, client, teamID, 2, sandboxtypes.StateRunning)

	// Stale index entry: ID in the team set without a sandbox key.
	require.NoError(t, client.SAdd(t.Context(), GetSandboxStorageTeamIndexKey(teamID.String()), "sbx-deleted").Err())

	items, err := storage.AllRunningItems(t.Context())
	require.NoError(t, err)
	assert.Len(t, items, 2)
}

// seedTeamSandboxes writes sandbox records + index entries directly (bypassing
// Storage.Add) and registers the team in the global teams index.
func seedTeamSandboxes(t *testing.T, client redis.UniversalClient, teamID uuid.UUID, count int, state sandboxtypes.State) {
	t.Helper()

	pipe := client.Pipeline()
	for i := range count {
		sbx := makeIndexedSandbox(teamID, fmt.Sprintf("sbx-%s-%04d", state, i), uuid.NewString(), time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
		sbx.State = state
		sbx.NodeID = "test-node"

		data, err := json.Marshal(sbx)
		require.NoError(t, err)

		pipe.Set(t.Context(), getSandboxKey(teamID.String(), sbx.SandboxID), data, 0)
		pipe.SAdd(t.Context(), GetSandboxStorageTeamIndexKey(teamID.String()), sbx.SandboxID)
	}
	pipe.ZAdd(t.Context(), globalTeamsSet, redis.Z{Score: float64(time.Now().Unix()), Member: teamID.String()})
	_, err := pipe.Exec(t.Context())
	require.NoError(t, err)
}
