package edge

import (
	"context"
	"errors"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/db/client"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	l "github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
	"github.com/e2b-dev/infra/packages/shared/pkg/synchronization"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const (
	clusterEndpointEnv = "LOCAL_CLUSTER_ENDPOINT"
	clusterTokenEnv    = "LOCAL_CLUSTER_TOKEN"

	poolSyncInterval = 60 * time.Second
	poolSyncTimeout  = 15 * time.Second
)

type Pool struct {
	db  *client.Client
	tel *telemetry.Client

	clusters        *smap.Map[*Cluster]
	synchronization *synchronization.Synchronize[queries.Cluster, *Cluster]
}

func localClusterConfig() (*queries.Cluster, error) {
	clusterEndpoint := os.Getenv(clusterEndpointEnv)
	if clusterEndpoint == "" {
		return nil, nil
	}

	clusterToken := os.Getenv(clusterTokenEnv)
	if clusterToken == "" {
		return nil, errors.New("no local cluster token provided")
	}

	return &queries.Cluster{
		ID:                 consts.LocalClusterID,
		EndpointTls:        false,
		Endpoint:           clusterEndpoint,
		Token:              clusterToken,
		SandboxProxyDomain: nil,
	}, nil
}

func NewPool(ctx context.Context, tel *telemetry.Client, db *client.Client) (*Pool, error) {
	p := &Pool{
		db:       db,
		tel:      tel,
		clusters: smap.New[*Cluster](),
	}

	// Shutdown function to gracefully close the pool
	go func() {
		<-ctx.Done()
		p.Close()
	}()

	localCluster, err := localClusterConfig()
	if err != nil {
		return nil, err
	}

	store := poolSynchronizationStore{pool: p, localCluster: localCluster}
	p.synchronization = synchronization.NewSynchronize("clusters-pool", "Clusters pool", store)

	// Periodically sync clusters with the database
	go p.synchronization.Start(poolSyncInterval, poolSyncTimeout, true)

	return p, nil
}

func (p *Pool) GetClusterById(id uuid.UUID) (*Cluster, bool) {
	return p.clusters.Get(id.String())
}

func (p *Pool) GetClusters() map[string]*Cluster {
	return p.clusters.Items()
}

func (p *Pool) Close() {
	p.synchronization.Close()

	wg := &sync.WaitGroup{}
	for _, cluster := range p.clusters.Items() {
		wg.Add(1)
		go func(c *Cluster) {
			defer wg.Done()
			zap.L().Info("Closing cluster", l.WithClusterID(c.ID))
			err := c.Close()
			if err != nil {
				zap.L().Error("Error closing cluster", zap.Error(err), l.WithClusterID(c.ID))
			}
		}(cluster)
	}
	wg.Wait()
}

// SynchronizationStore is an interface that defines methods for synchronizing the clusters pool with the database
type poolSynchronizationStore struct {
	pool         *Pool
	localCluster *queries.Cluster
}

func (d poolSynchronizationStore) SourceList(ctx context.Context) ([]queries.Cluster, error) {
	db, err := d.pool.db.GetActiveClusters(ctx)
	if err != nil {
		return nil, err
	}

	entries := make([]queries.Cluster, 0)
	for _, row := range db {
		entries = append(entries, row.Cluster)
	}

	// Append local cluster if registered
	if d.localCluster != nil {
		entries = append(entries, *d.localCluster)
	}

	return entries, nil
}

func (d poolSynchronizationStore) SourceExists(ctx context.Context, s []queries.Cluster, p *Cluster) bool {
	for _, item := range s {
		if item.ID == p.ID {
			return true
		}
	}

	return false
}

func (d poolSynchronizationStore) PoolList(ctx context.Context) []*Cluster {
	items := make([]*Cluster, 0)
	for _, item := range d.pool.clusters.Items() {
		items = append(items, item)
	}
	return items
}

func (d poolSynchronizationStore) PoolExists(ctx context.Context, cluster queries.Cluster) bool {
	_, found := d.pool.clusters.Get(cluster.ID.String())
	return found
}

func (d poolSynchronizationStore) PoolInsert(ctx context.Context, cluster queries.Cluster) {
	clusterID := cluster.ID.String()

	zap.L().Info("Initializing newly discovered cluster", l.WithClusterID(cluster.ID))

	c, err := NewCluster( //nolint:contextcheck // TODO: fix this later
		d.pool.tel,
		cluster.Endpoint,
		cluster.EndpointTls,
		cluster.Token,
		cluster.ID,
		cluster.SandboxProxyDomain,
	)
	if err != nil {
		zap.L().Error("Initializing cluster failed", zap.Error(err), l.WithClusterID(c.ID))
		return
	}

	zap.L().Info("Cluster initialized successfully", l.WithClusterID(c.ID))
	d.pool.clusters.Insert(clusterID, c)
}

func (d poolSynchronizationStore) PoolUpdate(ctx context.Context, cluster *Cluster) {
	// Clusters pool currently does not do something special during synchronization
}

func (d poolSynchronizationStore) PoolRemove(ctx context.Context, cluster *Cluster) {
	zap.L().Info("Removing cluster from pool", l.WithClusterID(cluster.ID))

	err := cluster.Close()
	if err != nil {
		zap.L().Error("Error during removing cluster from pool", zap.Error(err), l.WithClusterID(cluster.ID))
	}

	d.pool.clusters.Remove(cluster.ID.String())
}
