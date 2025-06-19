package edge

import (
	"context"
	"errors"
	"fmt"
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

			cluster, err := NewCluster(p.ctx, c.Endpoint, c.EndpointTls, c.Token, c.ID)
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
		go func(cluster *Cluster) {
			zap.L().Info("removing cluster from pool", l.WithClusterID(cluster.Id))
			cluster.Disconnect()
			p.pool.Remove(cluster.Id.String())
			wg.Done()
		}(cluster)
	}

	// wait for all not wanted clusters to be disconnected
	wg.Wait()

	return nil
}

type Cluster struct {
	ctx       context.Context
	ctxCancel context.CancelFunc

	Id     uuid.UUID
	Client *api.ClientWithResponses
}

func NewCluster(ctx context.Context, endpoint string, endpointTls bool, secret string, id uuid.UUID) (*Cluster, error) {
	// so we during cluster disconnect we don't cancel the upper context
	ctx, ctxCancel := context.WithCancel(ctx)

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

	// generate the full endpoint URL
	var endpointBaseUrl string
	if endpointTls {
		endpointBaseUrl = fmt.Sprintf("https://%s", endpoint)
	} else {
		endpointBaseUrl = fmt.Sprintf("http://%s", endpoint)
	}

	endpointClient, err := api.NewClientWithResponses(endpointBaseUrl, clientAuthMiddleware)
	if err != nil {
		ctxCancel()
		return nil, err
	}

	return &Cluster{
		ctx:       ctx,
		ctxCancel: ctxCancel,

		Id:     id,
		Client: endpointClient,
	}, nil
}

func (c *Cluster) Disconnect() {
	c.ctxCancel()
	<-c.ctx.Done()
}

func (c *Cluster) getTemplateBuilders() ([]*api.ClusterOrchestratorNode, error) {
	res, err := c.Client.V1ServiceDiscoveryGetOrchestratorsWithResponse(c.ctx)
	if err != nil {
		zap.L().Error("failed to get builders", zap.Error(err), l.WithClusterID(c.Id))
		return nil, err
	}

	if res.StatusCode() != http.StatusOK {
		return nil, fmt.Errorf("failed to get builders from api: %s", res.Status())
	}

	if res.JSON200 == nil {
		return nil, errors.New("request to get builders returned nil response")
	}

	builders := make([]*api.ClusterOrchestratorNode, 0)
	for _, o := range *res.JSON200 {
		if o.Status == api.Unhealthy || !slices.Contains(o.Roles, api.ClusterOrchestratorRoleTemplateManager) {
			continue
		}

		builders = append(builders, &o)
	}

	return builders, nil
}

func (c *Cluster) GetTemplateBuilderById(nodeId string) (*api.ClusterOrchestratorNode, error) {
	builders, err := c.getTemplateBuilders()
	if err != nil {
		return nil, err
	}

	for _, o := range builders {
		if o.Id == nodeId {
			return o, nil
		}
	}

	return nil, ErrTemplateBuilderNotFound
}

func (c *Cluster) GetAvailableTemplateBuilder() (*api.ClusterOrchestratorNode, error) {
	builders, err := c.getTemplateBuilders()
	if err != nil {
		return nil, err
	}

	// todo: for now we are returning first healthy one
	for _, o := range builders {
		if o.Status == api.Healthy {
			return o, nil
		}
	}

	return nil, ErrAvailableTemplateBuilderNotFound
}
