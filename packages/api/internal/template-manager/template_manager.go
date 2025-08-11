package template_manager

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	loki "github.com/grafana/loki/pkg/logcli/client"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"google.golang.org/grpc/metadata"

	templatecache "github.com/e2b-dev/infra/packages/api/internal/cache/templates"
	"github.com/e2b-dev/infra/packages/api/internal/edge"
	grpclient "github.com/e2b-dev/infra/packages/api/internal/grpc"
	buildlogs "github.com/e2b-dev/infra/packages/api/internal/template-manager/logs"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	sqlcdb "github.com/e2b-dev/infra/packages/db/client"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/db"
	infogrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator-info"
	templatemanagergrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

type processingBuilds struct {
	templateID string
}

type TemplateManager struct {
	grpc     *grpclient.GRPCClient
	edgePool *edge.Pool
	db       *db.DB

	lock          sync.Mutex
	tracer        trace.Tracer
	processing    map[uuid.UUID]processingBuilds
	buildCache    *templatecache.TemplatesBuildCache
	templateCache *templatecache.TemplateCache
	lokiClient    *loki.DefaultClient
	sqlcDB        *sqlcdb.Client

	localClient       *grpclient.GRPCClient
	localClientMutex  sync.RWMutex
	localClientStatus infogrpc.ServiceInfoStatus
}

type DeleteBuild struct {
	BuildID    uuid.UUID
	TemplateID string

	ClusterID     *uuid.UUID
	ClusterNodeID *string
}

const (
	syncInterval = time.Minute * 1
)

var ErrLocalTemplateManagerNotAvailable = errors.New("local template manager is not available")

func New(
	ctx context.Context,
	tracer trace.Tracer,
	tracerProvider trace.TracerProvider,
	meterProvider metric.MeterProvider,
	db *db.DB,
	sqlcDB *sqlcdb.Client,
	edgePool *edge.Pool,
	lokiClient *loki.DefaultClient,
	buildCache *templatecache.TemplatesBuildCache,
	templateCache *templatecache.TemplateCache,
) (*TemplateManager, error) {
	client, err := createClient(tracerProvider, meterProvider)
	if err != nil {
		return nil, fmt.Errorf("failed to establish GRPC connection: %w", err)
	}

	tm := &TemplateManager{
		grpc:          client,
		db:            db,
		sqlcDB:        sqlcDB,
		tracer:        tracer,
		buildCache:    buildCache,
		templateCache: templateCache,
		edgePool:      edgePool,
		lokiClient:    lokiClient,

		localClient:       client,
		localClientMutex:  sync.RWMutex{},
		localClientStatus: infogrpc.ServiceInfoStatus_Unhealthy,

		lock:       sync.Mutex{},
		processing: make(map[uuid.UUID]processingBuilds),
	}

	// Periodically check for local template manager health status
	go tm.localClientPeriodicHealthSync(ctx)

	return tm, nil
}

func (tm *TemplateManager) Close() error {
	return tm.grpc.Close()
}

func (tm *TemplateManager) BuildsStatusPeriodicalSync(ctx context.Context) {
	ticker := time.NewTicker(syncInterval)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			dbCtx, dbxCtxCancel := context.WithTimeout(ctx, 5*time.Second)
			buildsRunning, err := tm.sqlcDB.GetInProgressTemplateBuilds(dbCtx)
			if err != nil {
				zap.L().Error("Error getting running builds for periodical sync", zap.Error(err))
				dbxCtxCancel()
				continue
			}

			zap.L().Info("Running periodical sync of builds statuses", zap.Int("count", len(buildsRunning)))
			for _, b := range buildsRunning {
				go func(b queries.GetInProgressTemplateBuildsRow) {
					err := tm.BuildStatusSync(ctx, b.EnvBuild.ID, b.Env.ID, b.Team.ClusterID, b.EnvBuild.ClusterNodeID)
					if err != nil {
						zap.L().Error("Error syncing build status", zap.Error(err), zap.String("buildID", b.EnvBuild.ID.String()))
					}
				}(b)
			}

			dbxCtxCancel()
		}
	}
}

func (tm *TemplateManager) GetBuildClient(clusterID *uuid.UUID, nodeID *string, placement bool) (*BuildClient, error) {
	if clusterID == nil || nodeID == nil {
		return tm.GetLocalBuildClient(placement)
	} else {
		return tm.GetClusterBuildClient(*clusterID, *nodeID)
	}
}

func (tm *TemplateManager) GetAvailableBuildClient(ctx context.Context, clusterID *uuid.UUID) (*string, error) {
	if clusterID == nil {
		return nil, nil
	}

	cluster, ok := tm.edgePool.GetClusterById(*clusterID)
	if !ok {
		return nil, fmt.Errorf("cluster with ID '%s' not found", clusterID)
	}

	builder, err := cluster.GetAvailableTemplateBuilder(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get available template builder for cluster '%s': %w", clusterID, err)
	}

	builderNodeID := builder.NodeID
	return &builderNodeID, nil
}

func (tm *TemplateManager) GetLocalBuildClient(placement bool) (*BuildClient, error) {
	// build placement requires healthy template builder
	if placement && tm.GetLocalClientStatus() != infogrpc.ServiceInfoStatus_Healthy {
		zap.L().Error("Local template manager is not fully healthy, cannot use it for placement new builds")
		return nil, ErrLocalTemplateManagerNotAvailable
	}

	// for getting build information only not valid state is getting already unhealthy builder
	if tm.GetLocalClientStatus() == infogrpc.ServiceInfoStatus_Unhealthy {
		zap.L().Error("Local template manager is unhealthy")
		return nil, ErrLocalTemplateManagerNotAvailable
	}

	meta := metadata.New(map[string]string{})
	grpc := &edge.ClusterGRPC{Client: tm.grpc, Metadata: meta}

	logProviders := []buildlogs.Provider{
		&buildlogs.TemplateManagerProvider{GRPC: grpc},
		&buildlogs.LokiProvider{LokiClient: tm.lokiClient},
	}

	return &BuildClient{
		GRPC:         grpc,
		logProviders: logProviders,
	}, nil
}

func (tm *TemplateManager) GetClusterBuildClient(clusterID uuid.UUID, nodeID string) (*BuildClient, error) {
	cluster, ok := tm.edgePool.GetClusterById(clusterID)
	if !ok {
		return nil, errors.New("cluster not found")
	}

	instance, err := cluster.GetTemplateBuilderByNodeID(nodeID)
	if err != nil {
		return nil, fmt.Errorf("failed to get builder by id '%s': %w", nodeID, err)
	}

	grpc := cluster.GetGRPC(instance.ServiceInstanceID)
	http := cluster.GetHTTP(instance.NodeID)

	logProviders := []buildlogs.Provider{
		&buildlogs.TemplateManagerProvider{GRPC: grpc},
		&buildlogs.ClusterPlacementProvider{HTTP: http},
	}

	return &BuildClient{
		GRPC:         grpc,
		logProviders: logProviders,
	}, nil
}

func (tm *TemplateManager) DeleteBuild(ctx context.Context, t trace.Tracer, buildID uuid.UUID, templateID string, clusterID *uuid.UUID, clusterNodeID *string) error {
	ctx, span := t.Start(ctx, "delete-template",
		trace.WithAttributes(
			telemetry.WithBuildID(buildID.String()),
		),
	)
	defer span.End()

	client, err := tm.GetBuildClient(clusterID, clusterNodeID, false)
	if err != nil {
		return fmt.Errorf("failed to get builder edgeHttpClient: %w", err)
	}

	reqCtx := metadata.NewOutgoingContext(ctx, client.GRPC.Metadata)
	_, err = client.GRPC.Client.Template.TemplateBuildDelete(
		reqCtx, &templatemanagergrpc.TemplateBuildDeleteRequest{
			BuildID:    buildID.String(),
			TemplateID: templateID,
		},
	)

	err = utils.UnwrapGRPCError(err)
	if err != nil {
		return fmt.Errorf("failed to delete env build '%s': %w", buildID, err)
	}

	return nil
}

func (tm *TemplateManager) DeleteBuilds(ctx context.Context, builds []DeleteBuild) error {
	for _, build := range builds {
		err := tm.DeleteBuild(ctx, tm.tracer, build.BuildID, build.TemplateID, build.ClusterID, build.ClusterNodeID)
		if err != nil {
			return fmt.Errorf("failed to delete env build '%s': %w", build.BuildID, err)
		}
	}

	return nil
}

func (tm *TemplateManager) GetStatus(ctx context.Context, buildID uuid.UUID, templateID string, clusterID *uuid.UUID, clusterNodeID *string) (*templatemanagergrpc.TemplateBuildStatusResponse, error) {
	cli, err := tm.GetBuildClient(clusterID, clusterNodeID, false)
	if err != nil {
		return nil, fmt.Errorf("failed to get builder edgeHttpClient: %w", err)
	}

	reqCtx := metadata.NewOutgoingContext(ctx, cli.GRPC.Metadata)

	// error unwrapping is done in the caller
	return cli.GRPC.Client.Template.TemplateBuildStatus(
		reqCtx,
		&templatemanagergrpc.TemplateStatusRequest{
			BuildID: buildID.String(), TemplateID: templateID,
		},
	)
}
