package edge

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/db/client"
	"github.com/e2b-dev/infra/packages/db/queries"
	l "github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
	"github.com/e2b-dev/infra/packages/shared/pkg/synchronization"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const (
	poolSyncInterval = 60 * time.Second
	poolSyncTimeout  = 15 * time.Second
)

type Pool struct {
	db  *client.Client
	tel *telemetry.Client

	clusters        *smap.Map[*Cluster]
	synchronization *synchronization.Synchronize[queries.GetActiveClustersRow, *Cluster]

	tracer trace.Tracer
}

func NewPool(ctx context.Context, tel *telemetry.Client, db *client.Client, tracer trace.Tracer) (*Pool, error) {
	p := &Pool{
		db:       db,
		tel:      tel,
		tracer:   tracer,
		clusters: smap.New[*Cluster](),
	}

	// Shutdown function to gracefully close the pool
	go func() {
		<-ctx.Done()
		p.Close()
	}()

	store := poolSynchronizationStore{pool: p}
	p.synchronization = synchronization.NewSynchronize(p.tracer, "clusters-pool", "Clusters pool", store)

	// Periodically sync clusters with the database
	go p.synchronization.Start(poolSyncInterval, poolSyncTimeout, true)

	return p, nil
}

func (p *Pool) GetClusterById(id uuid.UUID) (*Cluster, bool) {
	cluster, ok := p.clusters.Get(id.String())
	if !ok {
		return nil, false
	}

	return cluster, true
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
	pool *Pool
}

func (d poolSynchronizationStore) SourceList(ctx context.Context) ([]queries.GetActiveClustersRow, error) {
	return d.pool.db.GetActiveClusters(ctx)
}

func (d poolSynchronizationStore) SourceExists(ctx context.Context, s []queries.GetActiveClustersRow, p *Cluster) bool {
	for _, item := range s {
		if item.Cluster.ID == p.ID {
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

func (d poolSynchronizationStore) PoolExists(ctx context.Context, source queries.GetActiveClustersRow) bool {
	_, found := d.pool.clusters.Get(source.Cluster.ID.String())
	return found
}

func (d poolSynchronizationStore) PoolInsert(ctx context.Context, source queries.GetActiveClustersRow) {
	cluster := source.Cluster
	clusterID := cluster.ID.String()

	zap.L().Info("Initializing newly discovered cluster", l.WithClusterID(cluster.ID))

	c, err := NewCluster(d.pool.tracer, d.pool.tel, cluster.Endpoint, cluster.EndpointTls, cluster.Token, cluster.ID)
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
