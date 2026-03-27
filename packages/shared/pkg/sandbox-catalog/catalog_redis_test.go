package sandbox_catalog

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	redis_utils "github.com/e2b-dev/infra/packages/shared/pkg/redis"
)

func TestRedisCatalog_LocalCache(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	redisClient := redis_utils.SetupInstance(t)

	sbxID := "sbx-local-cache"
	expected := &SandboxInfo{
		OrchestratorID:   "orch-1",
		OrchestratorIP:   "10.0.0.1",
		ExecutionID:      "exec-1",
		StartedAt:        time.Now().UTC().Truncate(time.Second),
		MaxLengthInHours: 2,
	}

	data, err := json.Marshal(expected)
	require.NoError(t, err)
	require.NoError(t, redisClient.Set(ctx, "sandbox:catalog:"+sbxID, data, time.Minute).Err())

	// Without local cache (noop) — reads from Redis every time.
	noCacheCatalog := NewRedisSandboxCatalog(redisClient, NewNoopSandboxCache())
	t.Cleanup(func() {
		assert.NoError(t, noCacheCatalog.Close(context.Background()))
	})

	got, err := noCacheCatalog.GetSandbox(ctx, sbxID)
	require.NoError(t, err)
	require.Equal(t, expected.OrchestratorID, got.OrchestratorID)
	require.Equal(t, expected.ExecutionID, got.ExecutionID)

	// With local cache — reads from Redis, populates cache.
	cache := NewReadThroughSandboxCache()
	cachedCatalog := NewRedisSandboxCatalog(redisClient, cache)
	t.Cleanup(func() {
		assert.NoError(t, cachedCatalog.Close(context.Background()))
	})

	got, err = cachedCatalog.GetSandbox(ctx, sbxID)
	require.NoError(t, err)
	require.Equal(t, expected.OrchestratorIP, got.OrchestratorIP)

	item := cache.Get(sbxID)
	require.NotNil(t, item, "local cache should be populated when enabled")
	require.Equal(t, expected.ExecutionID, item.Value().ExecutionID)
}
