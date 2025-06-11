package edge

import (
	"context"
	"errors"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"
	"slices"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/db/client"
	"github.com/e2b-dev/infra/packages/shared/pkg/http/edge"
)

const (
	poolSyncInterval = 60 * time.Second
)

var (
	ErrClusterNotFound = errors.New("cluster not found")
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

func (p *Pool) GetClusterByTeam(teamId uuid.UUID) (*Cluster, bool) {
	return p.pool.Get(teamId.String())
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
			} else {
				// todo: this can be removed later
				zap.L().Info("edge pool sync completed")
			}
		}
	}
}

func (p *Pool) sync(ctx context.Context) error {
	spanCtx, span := p.tracer.Start(ctx, "keep-in-sync")
	defer span.End()

	dbClusters, err := p.db.GetClustersWithTeams(spanCtx)
	if err != nil {
		return err
	}

	var wg sync.WaitGroup

	// connect newly discovered clusters
	zap.L().Info("syncing edges to db", zap.Int("count", len(dbClusters)))
	for _, dbCluster := range dbClusters {
		teamId := dbCluster.Team.ID.String()
		teamCluster := dbCluster.Cluster

		if _, ok := p.pool.Get(teamId); ok {
			// cluster already exists in the pool, skip it
			continue
		}

		// cluster initialization can take some time
		wg.Add(1)
		go func(team string, tc queries.Cluster) {
			logger := zap.L().With(
				zap.String("team_id", team),
				zap.String("cluster_id", tc.ID.String()),
			)

			logger.Info("initializing newly discovered cluster")

			cluster, err := NewCluster(p.ctx, teamCluster.Endpoint, teamCluster.Token, teamCluster.ID)
			if err != nil {
				logger.Error("initializing cluster failed", zap.Error(err))
			} else {
				logger.Info("cluster initialized successfully")
				p.pool.Insert(teamId, cluster)
			}

			wg.Done()
		}(teamId, teamCluster)
	}

	// wait for all clusters to be initialized
	wg.Wait()
	zap.L().Info("syncing newly discovered clusters finished")

	// cleanup removed clusters
	zap.L().Info("cleaning no more needed clusters")
	for teamId, cluster := range p.pool.Items() {
		found := false
		for _, dbCluster := range dbClusters {
			if dbCluster.Team.ID.String() == teamId {
				found = true
				break
			}
		}

		if found {
			continue
		}

		// cluster disconnect takes time
		wg.Add(1)
		go func(team string, teamCluster *Cluster) {
			zap.L().Info("removing cluster from pool", zap.String("team_id", teamId), zap.String("cluster_id", teamCluster.Id.String()))
			teamCluster.Disconnect()
			p.pool.Remove(team)
			wg.Done()
		}(teamId, cluster)
	}

	// wait for all not wanted clusters to be disconnected
	wg.Wait()
	zap.L().Info("clusters cleaning finished")

	return nil
}

type Cluster struct {
	ctx    context.Context
	secret string

	Id     uuid.UUID
	Client *api.ClientWithResponses
}

func NewCluster(ctx context.Context, endpoint string, secret string, id uuid.UUID) (*Cluster, error) {
	client, err := api.NewClientWithResponses(endpoint)
	if err != nil {
		return nil, err
	}

	// todo: maybe we should impl some middleware that will inject secret into every request?
	return &Cluster{
		ctx:    ctx,
		Id:     id.String(),
		Client: client,
		secret: secret,
	}, nil
}

func (c *Cluster) getOrchestrators(ctx context.Context) (*[]api.ClusterOrchestratorNode, error) {
	res, err := c.Client.V1ServiceDiscoveryGetOrchestratorsWithResponse(ctx)
	if err != nil {
		return nil, err
	}

	if res.JSON200 == nil {
		return nil, errors.New("failed to get template builders, response is nil")
	}

	return res.JSON200, nil
}

func (c *Cluster) GetTemplateBuilders(ctx context.Context) (*[]api.ClusterOrchestratorNode, error) {
	orchestrators, err := c.getOrchestrators(ctx)
	if err != nil {
		return nil, err
	}

	templateBuilders := make([]api.ClusterOrchestratorNode, 0)
	for _, orchestrator := range *orchestrators {
		if slices.Contains(orchestrator.Roles, api.ClusterOrchestratorRoleTemplateManager) {
			templateBuilders = append(templateBuilders, orchestrator)
		}
	}

	return &templateBuilders, nil
}

func (c *Cluster) Disconnect() {
	c.ctx.Done()
	// todo: what we should do here if template/sandbox is running?
	// theoretically nothing, because we are just shutting down api and we want to gracefully disconnect
}
