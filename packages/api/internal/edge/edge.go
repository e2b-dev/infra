package edge

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/db/client"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/http/edge"
	l "github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
)

const (
	edgeAuthHeader = "X-API-Key"

	poolSyncInterval = 60 * time.Second
)

var (
	ErrTemplateBuilderNotFound          = errors.New("template builder not found")
	ErrAvailableTemplateBuilderNotFound = errors.New("available template builder not found")
)

type Pool struct {
	db     *client.Client
	pool   *smap.Map[*Cluster]
	tracer trace.Tracer
	ctx    context.Context
}

func NewPool(ctx context.Context, db *client.Client, tracer trace.Tracer) (*Pool, error) {
	p := &Pool{
		ctx:    ctx,
		db:     db,
		tracer: tracer,
		pool:   smap.New[*Cluster](),
	}

	syncTimeout, syncCancel := context.WithTimeout(p.ctx, poolSyncInterval)
	defer syncCancel()

	err := p.sync(syncTimeout)
	if err != nil {
		zap.L().Error("failed to initialize edge pool", zap.Error(err))
		return nil, err
	}

	// periodically sync clusters with the database
	go func() { p.syncBackground() }()

	return p, nil
}

func (p *Pool) GetClusterById(clusterId uuid.UUID) (*Cluster, bool) {
	return p.pool.Get(clusterId.String())
}

func (p *Pool) syncBackground() {
	timer := time.NewTicker(poolSyncInterval)
	defer timer.Stop()

	for {
		select {
		case <-p.ctx.Done():
			zap.L().Info("edge pool sync ended")
			return
		case <-timer.C:
			syncTimeout, syncCancel := context.WithTimeout(p.ctx, poolSyncInterval)
			err := p.sync(syncTimeout)
			syncCancel()

			if err != nil {
				zap.L().Error("failed to sync edge pool", zap.Error(err))
			}
		}
	}
}

func (p *Pool) sync(ctx context.Context) error {
	spanCtx, span := p.tracer.Start(ctx, "keep-in-sync")
	defer span.End()

	// we want to fetch only clusters that are connected to teams
	dbClusters, err := p.db.GetActiveClusters(spanCtx)
	if err != nil {
		return err
	}

	var wg sync.WaitGroup

	// connect newly discovered clusters
	zap.L().Info("syncing edges to db", zap.Int("count", len(dbClusters)))
	for _, dbCluster := range dbClusters {
		clusterId := dbCluster.Cluster.ID.String()

		if _, ok := p.pool.Get(clusterId); ok {
			// cluster already exists in the pool, skip it
			continue
		}

		// cluster initialization can take some time
		wg.Add(1)
		go func(c queries.Cluster) {
			logger := zap.L().With(l.WithClusterID(c.ID))
			logger.Info("initializing newly discovered cluster")

			cluster, err := NewCluster(p.ctx, c.Endpoint, c.Token, c.ID)
			if err != nil {
				logger.Error("initializing cluster failed", zap.Error(err))
			} else {
				logger.Info("cluster initialized successfully")
				p.pool.Insert(clusterId, cluster)
			}

			wg.Done()
		}(dbCluster.Cluster)
	}

	// wait for all clusters to be initialized
	wg.Wait()

	// cleanup removed clusters
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
		go func(custer *Cluster) {
			zap.L().Info("removing cluster from pool", l.WithClusterID(cluster.Id))
			custer.Disconnect()
			p.pool.Remove(cluster.Id.String())
			wg.Done()
		}(cluster)
	}

	// wait for all not wanted clusters to be disconnected
	wg.Wait()

	return nil
}

type Cluster struct {
	ctx context.Context

	Id     uuid.UUID
	Client *api.ClientWithResponses
}

func NewCluster(ctx context.Context, endpoint string, secret string, id uuid.UUID) (*Cluster, error) {
	clientAuthMiddleware := func(c *api.Client) error {
		c.RequestEditors = append(
			c.RequestEditors,
			func(ctx context.Context, req *http.Request) error {
				req.Header.Set(edgeAuthHeader, secret)
				return nil
			},
		)
		return nil
	}

	client, err := api.NewClientWithResponses(endpoint, clientAuthMiddleware)
	if err != nil {
		return nil, err
	}

	return &Cluster{
		ctx: ctx,

		Id:     id,
		Client: client,
	}, nil
}

func (c *Cluster) Disconnect() {
	c.ctx.Done()
	// todo: what we should do here if template/sandbox is running?
	// theoretically nothing, because we are just shutting down api and we want to gracefully disconnect
}

func (c *Cluster) getTemplateBuilders(ctx context.Context) ([]*api.ClusterOrchestratorNode, error) {
	res, err := c.Client.V1ServiceDiscoveryGetOrchestratorsWithResponse(c.ctx)
	if err != nil {
		zap.L().Error("failed to get orchestrators", zap.Error(err), l.WithClusterID(c.Id))
		return nil, err
	}

	if res.JSON200 == nil {
		return nil, errors.New("api request failed")
	}

	orchestrators := make([]*api.ClusterOrchestratorNode, 0)
	for _, o := range *res.JSON200 {
		if o.Status == api.Unhealthy || !slices.Contains(o.Roles, api.ClusterOrchestratorRoleTemplateManager) {
			continue
		}

		orchestrators = append(orchestrators, &o)
	}

	return orchestrators, nil
}

func (c *Cluster) GetTemplateBuilderById(nodeId string) (*api.ClusterOrchestratorNode, error) {
	orchestrators, err := c.getTemplateBuilders(c.ctx)
	if err != nil {
		return nil, err
	}

	for _, o := range orchestrators {
		if o.Id == nodeId {
			return o, nil
		}
	}

	return nil, ErrTemplateBuilderNotFound
}

func (c *Cluster) GetAvailableTemplateBuilder() (*api.ClusterOrchestratorNode, error) {
	orchestrators, err := c.getTemplateBuilders(c.ctx)
	if err != nil {
		return nil, err
	}

	// todo: for now we are returning first healthy one
	for _, o := range orchestrators {
		if o.Status == api.Healthy {
			return o, nil
		}
	}

	return nil, ErrAvailableTemplateBuilderNotFound
}
