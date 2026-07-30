package clusters

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
)

func TestClusterCacheRevalidatesAndReplacesOnlyChangedConfig(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	clusterID := uuid.New()
	cached := queries.Cluster{
		ID:          clusterID,
		Name:        "managed",
		Endpoint:    "old.example.test:5008",
		EndpointTls: true,
		Token:       "old-token",
	}
	oldCluster := &Cluster{ID: clusterID}
	replacement := &Cluster{ID: clusterID}

	clusters := smap.New[*Cluster]()
	clusters.Insert(clusterID.String(), oldCluster)
	clusterConfigs := smap.New[queries.Cluster]()
	clusterConfigs.Insert(clusterID.String(), cached)
	currentConfigs := smap.New[queries.Cluster]()
	currentConfigs.Insert(clusterID.String(), cached)
	expiresAt := smap.New[time.Time]()
	expiresAt.Insert(clusterID.String(), now.Add(-time.Second))

	factoryCalls := 0
	closeCalls := 0
	store := clustersSyncStore{
		clusters:         clusters,
		clusterConfigs:   clusterConfigs,
		currentConfigs:   currentConfigs,
		clusterExpiresAt: expiresAt,
		now:              func() time.Time { return now },
		cacheTTL:         func() time.Duration { return clusterCacheTTL },
		clusterFactory: func(_ context.Context, config queries.Cluster) (*Cluster, error) {
			factoryCalls++
			require.Equal(t, "new.example.test:5008", config.Endpoint)

			return replacement, nil
		},
		clusterCloser: func(_ context.Context, cluster *Cluster) error {
			closeCalls++
			require.Same(t, oldCluster, cluster)

			return nil
		},
	}

	// An expired but unchanged entry is revalidated without recycling clients.
	store.PoolUpdate(t.Context(), oldCluster)
	require.Zero(t, factoryCalls)
	require.Zero(t, closeCalls)
	expiry, ok := expiresAt.Get(clusterID.String())
	require.True(t, ok)
	require.Equal(t, now.Add(clusterCacheTTL), expiry)
	require.Same(t, oldCluster, mustGetCluster(t, clusters, clusterID))

	// Before the renewed TTL, even a newly observed DB change is left alone.
	changed := cached
	changed.Endpoint = "new.example.test:5008"
	changed.Token = "new-token"
	currentConfigs.Insert(clusterID.String(), changed)
	store.PoolUpdate(t.Context(), oldCluster)
	require.Zero(t, factoryCalls)
	require.Same(t, oldCluster, mustGetCluster(t, clusters, clusterID))

	// Once expired, the changed config is built and atomically published before
	// the stale client is closed.
	expiresAt.Insert(clusterID.String(), now.Add(-time.Second))
	store.PoolUpdate(t.Context(), oldCluster)
	require.Equal(t, 1, factoryCalls)
	require.Equal(t, 1, closeCalls)
	require.Same(t, replacement, mustGetCluster(t, clusters, clusterID))

	storedConfig, ok := clusterConfigs.Get(clusterID.String())
	require.True(t, ok)
	require.Equal(t, changed, storedConfig)
}

func TestJitteredClusterCacheTTLBounds(t *testing.T) {
	t.Parallel()

	for range 100 {
		ttl := jitteredClusterCacheTTL()
		require.GreaterOrEqual(t, ttl, clusterCacheTTL)
		require.LessOrEqual(t, ttl, clusterCacheTTL+clusterCacheJitter)
	}
}

func TestClusterCacheRefreshFailureKeepsPublishedClient(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	clusterID := uuid.New()
	oldCluster := &Cluster{ID: clusterID}
	cached := queries.Cluster{ID: clusterID, Endpoint: "old.example.test:5008", Token: "old"}
	current := queries.Cluster{ID: clusterID, Endpoint: "new.example.test:5008", Token: "new"}

	clusters := smap.New[*Cluster]()
	clusters.Insert(clusterID.String(), oldCluster)
	clusterConfigs := smap.New[queries.Cluster]()
	clusterConfigs.Insert(clusterID.String(), cached)
	currentConfigs := smap.New[queries.Cluster]()
	currentConfigs.Insert(clusterID.String(), current)
	expiresAt := smap.New[time.Time]()
	expiredAt := now.Add(-time.Second)
	expiresAt.Insert(clusterID.String(), expiredAt)

	store := clustersSyncStore{
		clusters:         clusters,
		clusterConfigs:   clusterConfigs,
		currentConfigs:   currentConfigs,
		clusterExpiresAt: expiresAt,
		now:              func() time.Time { return now },
		cacheTTL:         func() time.Duration { return clusterCacheTTL },
		clusterFactory: func(context.Context, queries.Cluster) (*Cluster, error) {
			return nil, errors.New("invalid endpoint")
		},
		clusterCloser: func(context.Context, *Cluster) error {
			t.Fatal("stale cluster must not close when replacement initialization fails")

			return nil
		},
	}

	store.PoolUpdate(t.Context(), oldCluster)
	require.Same(t, oldCluster, mustGetCluster(t, clusters, clusterID))
	expiry, ok := expiresAt.Get(clusterID.String())
	require.True(t, ok)
	require.Equal(t, expiredAt, expiry, "expired deadline must remain so the next sync retries")
}

func mustGetCluster(t *testing.T, clusters *smap.Map[*Cluster], clusterID uuid.UUID) *Cluster {
	t.Helper()

	cluster, ok := clusters.Get(clusterID.String())
	require.True(t, ok)

	return cluster
}
