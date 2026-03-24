package sandbox_catalog

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/launchdarkly/go-server-sdk/v7/testhelpers/ldtestdata"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
	redis_utils "github.com/e2b-dev/infra/packages/shared/pkg/redis"
)

func TestRedisCatalog_LocalCacheFlagServiceContext(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	redisClient := redis_utils.SetupInstance(t)

	source := ldtestdata.DataSource()
	source.Update(
		source.Flag(featureflags.SandboxCatalogLocalCacheFlag.Key()).
			VariationForAll(true).
			VariationForKey(featureflags.ServiceKind, "client-proxy", false),
	)

	sbxID := "sbx-service-context"
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

	t.Run("service-targeted disable prevents local cache write", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()

		ff, err := featureflags.NewClientWithDatasource(source)
		require.NoError(t, err)
		ff.SetServiceName("client-proxy")
		t.Cleanup(func() {
			assert.NoError(t, ff.Close(context.Background()))
		})

		catalog := NewRedisSandboxesCatalog(redisClient, ff)
		t.Cleanup(func() {
			assert.NoError(t, catalog.Close(context.Background()))
		})

		got, err := catalog.GetSandbox(ctx, sbxID)
		require.NoError(t, err)
		require.Equal(t, expected.OrchestratorID, got.OrchestratorID)
		require.Equal(t, expected.ExecutionID, got.ExecutionID)

		assert.Nil(t, catalog.cache.Get(sbxID), "local cache should remain empty when flag is disabled for this service")
	})

	t.Run("other service uses local cache", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()

		ff, err := featureflags.NewClientWithDatasource(source)
		require.NoError(t, err)
		ff.SetServiceName("orchestration-api")
		t.Cleanup(func() {
			assert.NoError(t, ff.Close(context.Background()))
		})

		catalog := NewRedisSandboxesCatalog(redisClient, ff)
		t.Cleanup(func() {
			assert.NoError(t, catalog.Close(context.Background()))
		})

		got, err := catalog.GetSandbox(ctx, sbxID)
		require.NoError(t, err)
		require.Equal(t, expected.OrchestratorIP, got.OrchestratorIP)

		item := catalog.cache.Get(sbxID)
		require.NotNil(t, item, "local cache should be populated when flag is enabled")
		require.Equal(t, expected.ExecutionID, item.Value().ExecutionID)
	})
}
