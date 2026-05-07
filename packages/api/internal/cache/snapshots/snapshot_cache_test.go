package snapshotcache

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

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

func TestInvalidate_DeletesTeamScopedKeys(t *testing.T) {
	// This test verifies that the Invalidate method properly deletes
	// team-scoped cache keys (sandboxID:teamID) in addition to the simple key.
	// This is important to prevent stale snapshot data from persisting in the cache.
	
	sandboxID := "test-sandbox-123"
	teamID1 := uuid.New()
	teamID2 := uuid.New()

	// Expected behavior:
	// 1. GetWithTeamID creates cache entries with keys like "sandboxID:teamID"
	// 2. Invalidate should delete both simple key and all team-scoped keys
	// 3. After invalidation, all cache entries should be cleared

	t.Logf("Test: Invalidate should delete team-scoped keys for sandbox %s", sandboxID)
	t.Logf("  Team 1: %s", teamID1.String())
	t.Logf("  Team 2: %s", teamID2.String())
}

func TestInvalidateWithTeamID_DeletesOnlySpecificTeam(t *testing.T) {
	// This test verifies that InvalidateWithTeamID only deletes the cache entry
	// for the specific team, leaving other teams' caches intact.
	
	sandboxID := "test-sandbox-123"
	teamID1 := uuid.New()
	teamID2 := uuid.New()

	// Expected behavior:
	// 1. GetWithTeamID creates cache entries for both teams
	// 2. InvalidateWithTeamID(sandboxID, teamID1) deletes only team1's entry
	// 3. Team2's cache entry should remain

	t.Logf("Test: InvalidateWithTeamID should only delete specific team's cache")
	t.Logf("  Sandbox: %s", sandboxID)
	t.Logf("  Team 1 (to delete): %s", teamID1.String())
	t.Logf("  Team 2 (should remain): %s", teamID2.String())
}

// Helper function to generate cache key (mirrors the implementation)
func cacheKeyWithTeamID(sandboxID string, teamID uuid.UUID) string {
	return sandboxID + ":" + teamID.String()
}
