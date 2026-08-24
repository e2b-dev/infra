package sandbox_catalog

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	redis_utils "github.com/e2b-dev/infra/packages/shared/pkg/redis"
)

func testSandboxInfo(executionID, orchestratorID string) *SandboxInfo {
	return &SandboxInfo{
		OrchestratorID:   orchestratorID,
		OrchestratorIP:   "10.0.0.1",
		ExecutionID:      executionID,
		StartedAt:        time.Unix(0, 0).UTC(),
		MaxLengthInHours: 1,
	}
}

func TestRedisSandboxCatalog(t *testing.T) {
	t.Parallel()

	client := redis_utils.SetupInstance(t)
	catalog := NewRedisSandboxCatalog(client)
	ctx := t.Context()

	t.Run("store then get round-trips the info", func(t *testing.T) {
		t.Parallel()

		id := "sbx-roundtrip"
		want := testSandboxInfo("exec-1", "orch-A")
		require.NoError(t, catalog.StoreSandbox(ctx, id, want, time.Minute))

		got, err := catalog.GetSandbox(ctx, id)
		require.NoError(t, err)
		require.Equal(t, want.ExecutionID, got.ExecutionID)
		require.Equal(t, want.OrchestratorID, got.OrchestratorID)
	})

	t.Run("get on an absent key returns ErrSandboxNotFound", func(t *testing.T) {
		t.Parallel()

		_, err := catalog.GetSandbox(ctx, "sbx-absent")
		require.ErrorIs(t, err, ErrSandboxNotFound)
	})

	t.Run("delete removes the entry when the execution matches", func(t *testing.T) {
		t.Parallel()

		id := "sbx-delete-match"
		require.NoError(t, catalog.StoreSandbox(ctx, id, testSandboxInfo("exec-1", "orch-A"), time.Minute))
		require.NoError(t, catalog.DeleteSandbox(ctx, id, "exec-1"))

		_, err := catalog.GetSandbox(ctx, id)
		require.ErrorIs(t, err, ErrSandboxNotFound)
	})

	t.Run("delete keeps an entry owned by a different execution", func(t *testing.T) {
		t.Parallel()

		// The compare-and-delete guard: a stale teardown for exec-1 must not remove
		// the routing entry a newer exec-2 stored under the same id.
		id := "sbx-delete-mismatch"
		require.NoError(t, catalog.StoreSandbox(ctx, id, testSandboxInfo("exec-2", "orch-B"), time.Minute))
		require.NoError(t, catalog.DeleteSandbox(ctx, id, "exec-1"))

		got, err := catalog.GetSandbox(ctx, id)
		require.NoError(t, err)
		require.Equal(t, "exec-2", got.ExecutionID)
		require.Equal(t, "orch-B", got.OrchestratorID)
	})

	t.Run("delete on an absent key is a no-op", func(t *testing.T) {
		t.Parallel()

		require.NoError(t, catalog.DeleteSandbox(ctx, "sbx-never-stored", "exec-1"))
	})
}
