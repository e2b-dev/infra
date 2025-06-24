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
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const (
	poolSyncInterval = 60 * time.Second
)

type Pool struct {
	db     *client.Client
	pool   *smap.Map[*Cluster]
	tel    *telemetry.Client
	tracer trace.Tracer
	ctx    context.Context
}

func NewPool(ctx context.Context, tel *telemetry.Client, db *client.Client, tracer trace.Tracer) (*Pool, error) {
	p := &Pool{
		ctx:    ctx,
		db:     db,
		tel:    tel,
		tracer: tracer,
		pool:   smap.New[*Cluster](),
	}

	syncTimeout, syncCancel := context.WithTimeout(p.ctx, poolSyncInterval)
	defer syncCancel()

	err := p.sync(syncTimeout)
	if err != nil {
		zap.L().Error("Failed to initialize edge pool", zap.Error(err))
		return nil, err
	}

	// periodically sync clusters with the database
	go p.syncBackground()

	return p, nil
}

func (p *Pool) syncBackground() {
	timer := time.NewTicker(poolSyncInterval)
	defer timer.Stop()

	for {
		select {
		case <-p.ctx.Done():
			zap.L().Info("Clusters pool sync ended")
			return
		case <-timer.C:
			syncTimeout, syncCancel := context.WithTimeout(p.ctx, poolSyncInterval)
			err := p.sync(syncTimeout)
			syncCancel()

			if err != nil {
				zap.L().Error("Failed to sync edge pool", zap.Error(err))
			}
		}
	}
}

func (p *Pool) sync(ctx context.Context) error {
	spanCtx, span := p.tracer.Start(ctx, "keep-in-sync-clusters")
	defer span.End()

	// we want to fetch only clusters that are connected to teams
	dbClusters, err := p.db.GetActiveClusters(spanCtx)
	if err != nil {
		return err
	}

	var wg sync.WaitGroup

	// connect newly discovered clusters
	_, spanForNewlyDiscovered := p.tracer.Start(ctx, "keep-in-sync-newly-discovered-clusters")

	for _, dbCluster := range dbClusters {
		clusterId := dbCluster.Cluster.ID.String()

		if _, ok := p.pool.Get(clusterId); ok {
			// cluster already exists in the pool, skip it
			continue
		}

		// cluster initialization can take some time
		wg.Add(1)
		go func(c queries.Cluster) {
			zap.L().Info("Initializing newly discovered cluster", l.WithClusterID(c.ID))

			cluster, err := NewCluster(p.ctx, p.tracer, p.tel, c.Endpoint, c.EndpointTls, c.Token, c.ID)
			if err != nil {
				zap.L().Error("Initializing cluster failed", zap.Error(err), l.WithClusterID(c.ID))
			} else {
				zap.L().Info("Cluster initialized successfully", l.WithClusterID(c.ID))
				p.pool.Insert(clusterId, cluster)
			}

			wg.Done()
		}(dbCluster.Cluster)
	}
	spanForNewlyDiscovered.End()

	// wait for all clusters to be initialized
	wg.Wait()

	// cleanup removed clusters
	_, spanForOutdated := p.tracer.Start(ctx, "keep-in-sync-outdated-clusters")

	for clusterId, cluster := range p.pool.Items() {
		found := false
		for _, dbCluster := range dbClusters {
			if dbCluster.Cluster.ID.String() == clusterId {
				found = true
				break
			}
		}

		if found {
			continue
		}

		// cluster disconnect takes time
		wg.Add(1)
		go func(cluster *Cluster) {
			zap.L().Info("Removing cluster from pool", l.WithClusterID(cluster.Id))
			err := cluster.Disconnect()
			if err != nil {
				zap.L().Error("Error during removing cluster from pool", zap.Error(err), l.WithClusterID(cluster.Id))
			}
			p.pool.Remove(cluster.Id.String())
			wg.Done()
		}(cluster)
	}
	spanForOutdated.End()

	// wait for all not wanted clusters to be disconnected
	wg.Wait()

	return nil
}

func (p *Pool) GetClusterById(id uuid.UUID) (*Cluster, bool) {
	cluster, ok := p.pool.Get(id.String())
	if !ok {
		return nil, false
	}

	return cluster, true
}
