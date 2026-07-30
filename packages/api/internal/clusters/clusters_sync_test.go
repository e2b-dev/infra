package clusters

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
)

func TestClusterCacheRevalidatesAndReplacesOnlyChangedConfig(t *testing.T) {
	t.Parallel()

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

	factoryCalls := 0
	closeCalls := 0
	store := clustersSyncStore{
		clusters:       clusters,
		clusterConfigs: clusterConfigs,
		currentConfigs: currentConfigs,
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

	// An unchanged entry keeps its existing client.
	store.PoolUpdate(t.Context(), oldCluster)
	require.Zero(t, factoryCalls)
	require.Zero(t, closeCalls)
	require.Same(t, oldCluster, mustGetCluster(t, clusters, clusterID))

	// A newly observed DB change is applied on the current synchronization round.
	changed := cached
	changed.Endpoint = "new.example.test:5008"
	changed.Token = "new-token"
	currentConfigs.Insert(clusterID.String(), changed)
	store.PoolUpdate(t.Context(), oldCluster)
	require.Equal(t, 1, factoryCalls)
	require.Equal(t, 1, closeCalls)
	require.Same(t, replacement, mustGetCluster(t, clusters, clusterID))

	storedConfig, ok := clusterConfigs.Get(clusterID.String())
	require.True(t, ok)
	require.Equal(t, changed, storedConfig)
}

func TestClusterCacheRefreshFailureKeepsPublishedClient(t *testing.T) {
	t.Parallel()

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

	store := clustersSyncStore{
		clusters:       clusters,
		clusterConfigs: clusterConfigs,
		currentConfigs: currentConfigs,
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

	storedConfig, ok := clusterConfigs.Get(clusterID.String())
	require.True(t, ok)
	require.Equal(t, cached, storedConfig, "failed refresh must keep the cached config so the next sync retries")
}

func mustGetCluster(t *testing.T, clusters *smap.Map[*Cluster], clusterID uuid.UUID) *Cluster {
	t.Helper()

	cluster, ok := clusters.Get(clusterID.String())
	require.True(t, ok)

	return cluster
}
