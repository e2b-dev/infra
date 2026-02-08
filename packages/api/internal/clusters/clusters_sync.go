package clusters

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
	nomadapi "github.com/hashicorp/nomad/api"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/cfg"
	clickhouse "github.com/e2b-dev/infra/packages/clickhouse/pkg"
	"github.com/e2b-dev/infra/packages/db/client"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/logs/loki"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
	"github.com/e2b-dev/infra/packages/shared/pkg/synchronization"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const (
	clustersSyncInterval = 15 * time.Second
	clusterSyncTimeout   = 5 * time.Second
)

type Pool struct {
	db  *client.Client
	tel *telemetry.Client

	clusters        *smap.Map[*Cluster]
	synchronization *synchronization.Synchronize[queries.Cluster, *Cluster]
}

func localClusterConfig() (*queries.Cluster, error) {
	return &queries.Cluster{
		ID:                 consts.LocalClusterID,
		EndpointTls:        false,
		SandboxProxyDomain: nil,
	}, nil
}

func NewPool(
	ctx context.Context,
	tel *telemetry.Client,
	db *client.Client,
	nomad *nomadapi.Client,
	queryMetricsProvider clickhouse.Clickhouse,
	queryLogsProvider *loki.LokiQueryProvider,
	config cfg.Config,
) (*Pool, error) {
	clusters := smap.New[*Cluster]()

	localCluster, err := localClusterConfig()
	if err != nil {
		return nil, err
	}

	p := &Pool{
		db:       db,
		tel:      tel,
		clusters: clusters,
		synchronization: synchronization.NewSynchronize(
			"clusters-pool",
			"Clusters pool",
			clustersSyncStore{
				config:               config,
				db:                   db,
				tel:                  tel,
				clusters:             clusters,
				local:                localCluster,
				nomad:                nomad,
				queryLogsProvider:    queryLogsProvider,
				queryMetricsProvider: queryMetricsProvider,
			},
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
	nomad                *nomadapi.Client
	queryMetricsProvider clickhouse.Clickhouse
	queryLogsProvider    *loki.LokiQueryProvider
	config               cfg.Config
}

func (d clustersSyncStore) SourceList(ctx context.Context) ([]queries.Cluster, error) {
	db, err := d.db.GetActiveClusters(ctx)
	if err != nil {
		return nil, err
	}

	entries := make([]queries.Cluster, 0)
	for _, row := range db {
		entries = append(entries, row.Cluster)
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

	var c *Cluster
	var err error

	// Local cluster
	if cluster.ID == consts.LocalClusterID {
		c, err = newLocalCluster(context.WithoutCancel(ctx), d.tel, d.nomad, d.queryMetricsProvider, d.queryLogsProvider, d.config)
		if err != nil {
			logger.L().Error(ctx, "Initializing local cluster failed", zap.Error(err), logger.WithClusterID(cluster.ID))

			return
		}

		d.clusters.Insert(clusterID, c)
		logger.L().Info(ctx, "Local cluster initialized successfully", logger.WithClusterID(cluster.ID))

		return
	}

	// Remote cluster
	c, err = newRemoteCluster(context.WithoutCancel(ctx), d.tel, cluster.Endpoint, cluster.EndpointTls, cluster.Token, cluster.ID, cluster.SandboxProxyDomain)
	if err != nil {
		logger.L().Error(ctx, "Initializing remote cluster failed", zap.Error(err), logger.WithClusterID(cluster.ID))

		return
	}

	d.clusters.Insert(clusterID, c)
	logger.L().Info(ctx, "Remote cluster initialized successfully", logger.WithClusterID(cluster.ID))
}

func (d clustersSyncStore) PoolUpdate(_ context.Context, _ *Cluster) {
	// Clusters pool currently does not do something special during synchronization
}

func (d clustersSyncStore) PoolRemove(ctx context.Context, cluster *Cluster) {
	logger.L().Info(ctx, "Removing cluster from pool", logger.WithClusterID(cluster.ID))

	err := cluster.Close(ctx)
	if err != nil {
		logger.L().Error(ctx, "Error during removing cluster from pool", zap.Error(err), logger.WithClusterID(cluster.ID))
	}

	d.clusters.Remove(cluster.ID.String())
}
