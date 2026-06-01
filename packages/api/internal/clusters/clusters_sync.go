package clusters

import (
	"context"
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
	cfg cfg.Config,
) (*Pool, error) {
	clusters := smap.New[*Cluster]()
	clusterCreateFunc := func(ctx context.Context, source queries.Cluster) (*Cluster, error) {
		// Local cluster
		if source.ID == consts.LocalClusterID {
			return newLocalCluster(context.WithoutCancel(ctx), tel, localDiscovery, queryMetricsProvider, queryLogsProvider, cfg), nil
		}

		// Remote cluster
		authOrgID := ""
		if source.AuthOrgID != nil {
			authOrgID = *source.AuthOrgID
		}

		config := clusterConfig{
			endpoint:      source.Endpoint,
			endpointTLS:   source.EndpointTls,
			token:         source.Token,
			sandboxDomain: source.SandboxProxyDomain,
			oauthOrgID:    authOrgID,
		}

		c, err := newRemoteCluster(context.WithoutCancel(ctx), tel, source.ID, config)
		if err != nil {
			return nil, err
		}

		return c, nil
	}

	p := &Pool{
		db:       db,
		tel:      tel,
		clusters: clusters,
		synchronization: synchronization.NewSynchronize(
			"clusters-pool",
			"Clusters pool",
			clustersSyncStore{
				db:                db,
				clusters:          clusters,
				clusterCreateFunc: clusterCreateFunc,
				local:             localClusterConfig(),
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
	db                *client.Client
	local             *queries.Cluster
	clusters          *smap.Map[*Cluster]
	clusterCreateFunc func(context.Context, queries.Cluster) (*Cluster, error)
}

func (d clustersSyncStore) SourceList(ctx context.Context) ([]queries.Cluster, error) {
	db, err := d.db.GetActiveClusters(ctx)
	if err != nil {
		return nil, err
	}

	entries := make([]queries.Cluster, 0, len(db))
	for _, row := range db {
		entries = append(entries, row.Cluster)
	}

	// Append local cluster if provided
	if d.local != nil {
		entries = append(entries, *d.local)
	}

	return entries, nil
}

func (d clustersSyncStore) SourceGet(_ context.Context, s []queries.Cluster, p *Cluster) (queries.Cluster, bool) {
	for _, item := range s {
		if item.ID == p.ID {
			return item, true
		}
	}

	return queries.Cluster{}, false
}

func (d clustersSyncStore) PoolList(_ context.Context) []*Cluster {
	items := make([]*Cluster, 0)
	for _, item := range d.clusters.Items() {
		items = append(items, item)
	}

	return items
}

func (d clustersSyncStore) PoolGet(_ context.Context, source queries.Cluster) (*Cluster, bool) {
	return d.clusters.Get(source.ID.String())
}

func (d clustersSyncStore) PoolInsert(ctx context.Context, source queries.Cluster) {
	clusterID := source.ID

	logger.L().Info(ctx, "Initializing newly discovered cluster", logger.WithClusterID(clusterID))

	c, err := d.clusterCreateFunc(ctx, source)
	if err != nil {
		logger.L().Error(ctx, "Error during initializing newly discovered cluster", zap.Error(err), logger.WithClusterID(clusterID))

		return
	}

	d.clusters.Insert(clusterID.String(), c)

	logger.L().Info(ctx, "Cluster initialized successfully", logger.WithClusterID(clusterID))
}

func (d clustersSyncStore) PoolUpdate(_ context.Context, e *Cluster, _ queries.Cluster) {}

func (d clustersSyncStore) PoolRemove(ctx context.Context, cluster *Cluster) {
	logger.L().Info(ctx, "Removing cluster from pool", logger.WithClusterID(cluster.ID))

	err := cluster.Close(ctx)
	if err != nil {
		logger.L().Error(ctx, "Error during removing cluster from pool", zap.Error(err), logger.WithClusterID(cluster.ID))
	}

	d.clusters.Remove(cluster.ID.String())
}
