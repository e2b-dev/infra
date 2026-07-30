package clusters

import (
	"context"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/cfg"
	"github.com/e2b-dev/infra/packages/api/internal/clusters/discovery"
	clickhouse "github.com/e2b-dev/infra/packages/clickhouse/pkg"
	"github.com/e2b-dev/infra/packages/db/client"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/logs/loki"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
	"github.com/e2b-dev/infra/packages/shared/pkg/synchronization"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const (
	clustersSyncInterval = 15 * time.Second
	clusterSyncTimeout   = 5 * time.Second
	clusterCacheTTL      = 5 * time.Minute
	clusterCacheJitter   = 30 * time.Second
)

type Pool struct {
	db  *client.Client
	tel *telemetry.Client

	clusters        *smap.Map[*Cluster]
	synchronization *synchronization.Synchronize[queries.Cluster, *Cluster]
}

func localClusterConfig() *queries.Cluster {
	return &queries.Cluster{
		ID:                 consts.LocalClusterID,
		EndpointTls:        false,
		SandboxProxyDomain: nil,
	}
}

func NewPool(
	ctx context.Context,
	tel *telemetry.Client,
	db *client.Client,
	localDiscovery discovery.Discovery,
	queryMetricsProvider clickhouse.Clickhouse,
	queryLogsProvider *loki.LokiQueryProvider,
	sandboxLogsReader ClickhouseLogsReader,
	featureFlags *featureflags.Client,
	config cfg.Config,
) (*Pool, error) {
	clusters := smap.New[*Cluster]()
	clusterConfigs := smap.New[queries.Cluster]()
	currentConfigs := smap.New[queries.Cluster]()
	clusterExpiresAt := smap.New[time.Time]()

	localCluster := localClusterConfig()

	store := clustersSyncStore{
		config:               config,
		db:                   db,
		tel:                  tel,
		clusters:             clusters,
		clusterConfigs:       clusterConfigs,
		currentConfigs:       currentConfigs,
		clusterExpiresAt:     clusterExpiresAt,
		local:                localCluster,
		localDiscovery:       localDiscovery,
		queryLogsProvider:    queryLogsProvider,
		queryMetricsProvider: queryMetricsProvider,
		sandboxLogsReader:    sandboxLogsReader,
		featureFlags:         featureFlags,
		now:                  time.Now,
		cacheTTL:             jitteredClusterCacheTTL,
	}

	p := &Pool{
		db:       db,
		tel:      tel,
		clusters: clusters,
		synchronization: synchronization.NewSynchronize(
			"clusters-pool",
			"Clusters pool",
			store,
		),
	}

	// Periodically sync clusters with the database
	go p.synchronization.Start(ctx, clustersSyncInterval, clusterSyncTimeout, true)

	return p, nil
}

func (p *Pool) GetClusterById(id uuid.UUID) (*Cluster, bool) {
	return p.clusters.Get(id.String())
}

func (p *Pool) GetClusters() map[string]*Cluster {
	return p.clusters.Items()
}

func (p *Pool) Close(ctx context.Context) {
	p.synchronization.Close()

	wg := &sync.WaitGroup{}
	for _, cluster := range p.clusters.Items() {
		wg.Go(func() {
			logger.L().Info(ctx, "Closing cluster", logger.WithClusterID(cluster.ID))
			err := cluster.Close(ctx)
			if err != nil {
				logger.L().Error(ctx, "Error closing cluster", zap.Error(err), logger.WithClusterID(cluster.ID))
			}
		})
	}
	wg.Wait()
}

// SynchronizationStore is an interface that defines methods for synchronizing the clusters pool with the database
type clustersSyncStore struct {
	db                   *client.Client
	tel                  *telemetry.Client
	clusters             *smap.Map[*Cluster]
	local                *queries.Cluster
	localDiscovery       discovery.Discovery
	queryMetricsProvider clickhouse.Clickhouse
	queryLogsProvider    *loki.LokiQueryProvider
	sandboxLogsReader    ClickhouseLogsReader
	featureFlags         *featureflags.Client
	config               cfg.Config
	clusterConfigs       *smap.Map[queries.Cluster]
	currentConfigs       *smap.Map[queries.Cluster]
	clusterExpiresAt     *smap.Map[time.Time]
	now                  func() time.Time
	cacheTTL             func() time.Duration
	clusterFactory       func(context.Context, queries.Cluster) (*Cluster, error)
	clusterCloser        func(context.Context, *Cluster) error
}

func (d clustersSyncStore) SourceList(ctx context.Context) ([]queries.Cluster, error) {
	db, err := d.db.GetActiveClusters(ctx)
	if err != nil {
		return nil, err
	}

	entries := make([]queries.Cluster, 0)
	for _, row := range db {
		entries = append(entries, row.Cluster)
		d.currentConfigs.Insert(row.Cluster.ID.String(), row.Cluster)
	}

	// Append local cluster if provided
	if d.local != nil {
		entries = append(entries, *d.local)
	}

	return entries, nil
}

func (d clustersSyncStore) SourceExists(_ context.Context, s []queries.Cluster, p *Cluster) bool {
	for _, item := range s {
		if item.ID == p.ID {
			return true
		}
	}

	return false
}

func (d clustersSyncStore) PoolList(_ context.Context) []*Cluster {
	items := make([]*Cluster, 0)
	for _, item := range d.clusters.Items() {
		items = append(items, item)
	}

	return items
}

func (d clustersSyncStore) PoolExists(_ context.Context, cluster queries.Cluster) bool {
	_, found := d.clusters.Get(cluster.ID.String())

	return found
}

func (d clustersSyncStore) PoolInsert(ctx context.Context, cluster queries.Cluster) {
	clusterID := cluster.ID.String()

	logger.L().Info(ctx, "Initializing newly discovered cluster", logger.WithClusterID(cluster.ID))

	c, err := d.initializeCluster(context.WithoutCancel(ctx), cluster)
	if err != nil {
		logger.L().Error(ctx, "Initializing remote cluster failed", zap.Error(err), logger.WithClusterID(cluster.ID))

		return
	}

	d.clusters.Insert(clusterID, c)
	d.clusterConfigs.Insert(clusterID, cluster)
	if cluster.ID != consts.LocalClusterID {
		d.clusterExpiresAt.Insert(clusterID, d.nextClusterExpiry())
	}
	logger.L().Info(ctx, "Cluster initialized successfully", logger.WithClusterID(cluster.ID))
}

func (d clustersSyncStore) PoolUpdate(ctx context.Context, cluster *Cluster) {
	if cluster.ID == consts.LocalClusterID {
		return
	}

	clusterID := cluster.ID.String()
	expiresAt, ok := d.clusterExpiresAt.Get(clusterID)
	if ok && d.now().Before(expiresAt) {
		return
	}

	current, ok := d.currentConfigs.Get(clusterID)
	if !ok {
		return
	}

	cached, ok := d.clusterConfigs.Get(clusterID)
	if ok && sameClusterConfig(cached, current) {
		d.clusterExpiresAt.Insert(clusterID, d.nextClusterExpiry())

		return
	}

	replacement, err := d.initializeCluster(context.WithoutCancel(ctx), current)
	if err != nil {
		logger.L().Error(ctx, "Refreshing cached cluster failed", zap.Error(err), logger.WithClusterID(cluster.ID))

		return
	}

	// Publish the replacement before closing the stale client so readers never
	// observe the cluster as missing.
	d.clusters.Insert(clusterID, replacement)
	d.clusterConfigs.Insert(clusterID, current)
	d.clusterExpiresAt.Insert(clusterID, d.nextClusterExpiry())

	if err := d.closeCluster(ctx, cluster); err != nil {
		logger.L().Error(ctx, "Closing stale cluster failed", zap.Error(err), logger.WithClusterID(cluster.ID))
	}
	logger.L().Info(ctx, "Refreshed cached cluster", logger.WithClusterID(cluster.ID))
}

func (d clustersSyncStore) PoolRemove(ctx context.Context, cluster *Cluster) {
	logger.L().Info(ctx, "Removing cluster from pool", logger.WithClusterID(cluster.ID))

	err := cluster.Close(ctx)
	if err != nil {
		logger.L().Error(ctx, "Error during removing cluster from pool", zap.Error(err), logger.WithClusterID(cluster.ID))
	}

	d.clusters.Remove(cluster.ID.String())
	d.clusterConfigs.Remove(cluster.ID.String())
	d.currentConfigs.Remove(cluster.ID.String())
	d.clusterExpiresAt.Remove(cluster.ID.String())
}

func (d clustersSyncStore) initializeCluster(ctx context.Context, cluster queries.Cluster) (*Cluster, error) {
	if d.clusterFactory != nil {
		return d.clusterFactory(ctx, cluster)
	}

	if cluster.ID == consts.LocalClusterID {
		return newLocalCluster(ctx, d.tel, d.localDiscovery, d.queryMetricsProvider, d.queryLogsProvider, d.sandboxLogsReader, d.featureFlags, d.config), nil
	}

	authOrgID := ""
	if cluster.AuthOrgID != nil {
		authOrgID = *cluster.AuthOrgID
	}

	return newRemoteCluster(
		ctx,
		d.tel,
		cluster.Endpoint,
		cluster.EndpointTls,
		cluster.Token,
		cluster.ID,
		cluster.SandboxProxyDomain,
		authOrgID,
	)
}

func (d clustersSyncStore) closeCluster(ctx context.Context, cluster *Cluster) error {
	if d.clusterCloser != nil {
		return d.clusterCloser(ctx, cluster)
	}

	return cluster.Close(ctx)
}

func (d clustersSyncStore) nextClusterExpiry() time.Time {
	ttl := clusterCacheTTL
	if d.cacheTTL != nil {
		ttl = d.cacheTTL()
	}

	return d.now().Add(ttl)
}

func jitteredClusterCacheTTL() time.Duration {
	return clusterCacheTTL + time.Duration(rand.Int64N(int64(clusterCacheJitter)+1))
}

func sameClusterConfig(left, right queries.Cluster) bool {
	return left.ID == right.ID &&
		left.Endpoint == right.Endpoint &&
		left.EndpointTls == right.EndpointTls &&
		left.Token == right.Token &&
		sameOptionalString(left.SandboxProxyDomain, right.SandboxProxyDomain) &&
		sameOptionalString(left.AuthOrgID, right.AuthOrgID) &&
		left.Name == right.Name
}

func sameOptionalString(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}

	return *left == *right
}
