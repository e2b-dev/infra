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
	db       *client.Client
	tel      *telemetry.Client
	clusters *smap.Map[*Cluster]
	tracer   trace.Tracer

	close chan struct{}
}

func NewPool(ctx context.Context, tel *telemetry.Client, db *client.Client, tracer trace.Tracer) (*Pool, error) {
	p := &Pool{
		db:       db,
		tel:      tel,
		tracer:   tracer,
		clusters: smap.New[*Cluster](),
		close:    make(chan struct{}),
	}

	// Periodically sync clusters with the database
	go p.syncBackground()

	// Shutdown function to gracefully close the pool
	go func() {
		<-ctx.Done()
		p.Close()
	}()

	return p, nil
}

func (p *Pool) syncBackground() {
	synchronize := synchronization.Synchronize[queries.GetActiveClustersRow, string, *Cluster]{
		Tracer:           p.tracer,
		TracerSpanPrefix: "clusters-pool",
		Store:            poolSynchronizationStore{pool: p},
	}

	synchronize.SyncInBackground(p.close, poolSyncInterval, poolSyncTimeout, true)
}

func (p *Pool) GetClusterById(id uuid.UUID) (*Cluster, bool) {
	cluster, ok := p.clusters.Get(id.String())
	if !ok {
		return nil, false
	}

	return cluster, true
}

func (p *Pool) Close() {
	// Close pool, this needs to be called before closing the clusters
	// so background jobs syncing cluster will not try to update the pool nodes
	close(p.close)

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

func (d poolSynchronizationStore) SourceKey(item queries.GetActiveClustersRow) string {
	return item.Cluster.ID.String()
}

func (d poolSynchronizationStore) PoolList(ctx context.Context) map[string]*Cluster {
	return d.pool.clusters.Items()
}

func (d poolSynchronizationStore) PoolExists(ctx context.Context, key string) bool {
	_, found := d.pool.clusters.Get(key)
	return found
}

func (d poolSynchronizationStore) PoolInsert(ctx context.Context, clusterID string, clusterValue queries.GetActiveClustersRow) error {
	clusterRow := clusterValue.Cluster
	zap.L().Info("Initializing newly discovered cluster", l.WithClusterID(clusterRow.ID))

	c, err := NewCluster(d.pool.tracer, d.pool.tel, clusterRow.Endpoint, clusterRow.EndpointTls, clusterRow.Token, clusterRow.ID)
	if err != nil {
		zap.L().Error("Initializing cluster failed", zap.Error(err), l.WithClusterID(c.ID))
	} else {
		zap.L().Info("Cluster initialized successfully", l.WithClusterID(c.ID))
		d.pool.clusters.Insert(clusterID, c)
	}

	return nil
}

// PoolSynchronize - Item exists in the pool, calling this method so custom logic can be applied
func (d poolSynchronizationStore) PoolSynchronize(ctx context.Context, clusterID string, cluster *Cluster) {
	// todo
}

func (d poolSynchronizationStore) PoolRemove(ctx context.Context, cluster *Cluster) error {
	zap.L().Info("Removing cluster from pool", l.WithClusterID(cluster.ID))
	err := cluster.Close()
	if err != nil {
		zap.L().Error("Error during removing cluster from pool", zap.Error(err), l.WithClusterID(cluster.ID))
	}

	d.pool.clusters.Remove(cluster.ID.String())
	return nil
}
