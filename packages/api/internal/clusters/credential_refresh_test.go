package clusters

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	"github.com/e2b-dev/infra/packages/shared/pkg/synchronization"
)

type credentialRefreshSource struct {
	clustersSyncStore

	rows []queries.Cluster
}

func (s *credentialRefreshSource) SourceList(context.Context) ([]queries.Cluster, error) {
	return s.rows, nil
}

func TestClusterSynchronizationRefreshesRemoteCredential(t *testing.T) {
	t.Parallel()
	var oldRequests, replacementRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Header.Get(consts.EdgeApiAuthHeader) {
		case "old-test-token":
			oldRequests.Add(1)
		case "replacement-test-token":
			replacementRequests.Add(1)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"orchestrators":[]}`))
	}))
	t.Cleanup(server.Close)
	ctx, cancel := context.WithCancel(t.Context())
	pool := newLifecycleTestPool(ctx, cancel)
	t.Cleanup(func() { pool.Close(t.Context()) })
	pool.store.activateCluster = func(cluster *Cluster, _ initialSyncMode) error {
		return pool.activateCluster(ctx, cluster, syncOnNextTick)
	}
	source := &credentialRefreshSource{clustersSyncStore: pool.store, rows: []queries.Cluster{{
		ID: uuid.New(), Endpoint: strings.TrimPrefix(server.URL, "http://"), Token: "old-test-token",
	}}}
	syncer := synchronization.NewSynchronize("credential-refresh", "Credential refresh", source)
	require.NoError(t, syncer.Sync(ctx))
	old, ok := pool.clusters.Get(source.rows[0].ID.String())
	require.True(t, ok)
	require.NoError(t, old.SyncInstances(ctx))
	require.Positive(t, oldRequests.Load())
	require.NoError(t, syncer.Sync(ctx))
	unchanged, _ := pool.clusters.Get(old.ID.String())
	require.Same(t, old, unchanged)

	connection, err := grpc.NewClient("passthrough:///unused", grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = connection.Close() })
	old.instances.Insert("old-instance", &Instance{client: NewGRPCClient(connection, "test")})
	source.rows[0].Token = "replacement-test-token"
	// The first round retires the old client; the next discovers its replacement.
	require.NoError(t, syncer.Sync(ctx))
	require.Zero(t, pool.clusters.Count())
	require.Equal(t, connectivity.Shutdown, connection.GetState())
	require.NoError(t, syncer.Sync(ctx))
	replacement, ok := pool.clusters.Get(old.ID.String())
	require.True(t, ok)
	require.NotSame(t, old, replacement)
	require.NoError(t, replacement.SyncInstances(ctx))
	require.Positive(t, replacementRequests.Load())

	// Local discovery does not use the database edge credential.
	require.True(t, source.SourceExists(ctx, []queries.Cluster{{ID: consts.LocalClusterID, Token: "unused"}}, &Cluster{ID: consts.LocalClusterID}))
}
