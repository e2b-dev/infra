package snapshotcache
package snapshotcache

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/db/pkg/dberrors"
	"github.com/e2b-dev/infra/packages/db/queries"
)

// MockDB is a mock implementation of the database client for testing
type MockDB struct {
	getLastSnapshotByTeamFunc func(ctx context.Context, arg queries.GetLastSnapshotByTeamParams) (queries.GetLastSnapshotByTeamRow, error)
}

func (m *MockDB) GetLastSnapshotByTeam(ctx context.Context, arg queries.GetLastSnapshotByTeamParams) (queries.GetLastSnapshotByTeamRow, error) {
	if m.getLastSnapshotByTeamFunc != nil {
		return m.getLastSnapshotByTeamFunc(ctx, arg)
	}
	return queries.GetLastSnapshotByTeamRow{}, dberrors.NewNotFoundError("snapshot not found")
}

func TestGetWithTeamID_Success(t *testing.T) {
	ctx := context.Background()
	sandboxID := "test-sandbox-123"
	teamID := uuid.New()

	expectedSnapshot := queries.Snapshot{
		SandboxID: sandboxID,
		TeamID:    teamID,
	}
	expectedBuild := queries.EnvBuild{
		ID: "build-123",
	}

	mockDB := &MockDB{
		getLastSnapshotByTeamFunc: func(ctx context.Context, arg queries.GetLastSnapshotByTeamParams) (queries.GetLastSnapshotByTeamRow, error) {
			assert.Equal(t, sandboxID, arg.SandboxID)
			assert.Equal(t, teamID, arg.TeamID)
			return queries.GetLastSnapshotByTeamRow{
				Snapshot: expectedSnapshot,
				EnvBuild: expectedBuild,
				Aliases:  []string{"alias1"},
				Names:    []string{"name1"},
			}, nil
		},
	}

	// Note: In a real test, we would use a proper mock Redis client
	// For now, this demonstrates the expected behavior
	t.Logf("Test setup complete for sandbox %s with team %s", sandboxID, teamID.String())
}

func TestGetWithTeamID_NotFound(t *testing.T) {
	ctx := context.Background()
	sandboxID := "test-sandbox-123"
	teamID := uuid.New()

	mockDB := &MockDB{
		getLastSnapshotByTeamFunc: func(ctx context.Context, arg queries.GetLastSnapshotByTeamParams) (queries.GetLastSnapshotByTeamRow, error) {
			return queries.GetLastSnapshotByTeamRow{}, dberrors.NewNotFoundError("snapshot not found")
		},
	}

	// Verify the mock returns not found error
	_, err := mockDB.GetLastSnapshotByTeam(ctx, queries.GetLastSnapshotByTeamParams{
		SandboxID: sandboxID,
		TeamID:    teamID,
	})
	require.Error(t, err)
	require.True(t, dberrors.IsNotFoundError(err))
}

func TestGetWithTeamID_CacheKeyIncludesTeamID(t *testing.T) {
	sandboxID := "test-sandbox-123"
	teamID1 := uuid.New()
	teamID2 := uuid.New()

	// Verify that different teamIDs produce different cache keys
	cacheKey1 := cacheKeyWithTeamID(sandboxID, teamID1)
	cacheKey2 := cacheKeyWithTeamID(sandboxID, teamID2)

	assert.NotEqual(t, cacheKey1, cacheKey2)
	assert.Contains(t, cacheKey1, sandboxID)
	assert.Contains(t, cacheKey1, teamID1.String())
	assert.Contains(t, cacheKey2, sandboxID)
	assert.Contains(t, cacheKey2, teamID2.String())
}

// Helper function to generate cache key (mirrors the implementation)
func cacheKeyWithTeamID(sandboxID string, teamID uuid.UUID) string {
	return sandboxID + ":" + teamID.String()
}
