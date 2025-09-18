package template_manager

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
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
	templatemanagergrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

var tracer = otel.Tracer("github.com/e2b-dev/infra/packages/api/internal/template-manager")

type processingBuilds struct {
	templateID string
}

type TemplateManager struct {
	grpc     *grpclient.GRPCClient
	edgePool *edge.Pool
	db       *db.DB

	lock          sync.Mutex
	processing    map[uuid.UUID]processingBuilds
	buildCache    *templatecache.TemplatesBuildCache
	templateCache *templatecache.TemplateCache
	sqlcDB        *sqlcdb.Client
}

type DeleteBuild struct {
	BuildID    uuid.UUID
	TemplateID string

	ClusterID uuid.UUID
	NodeID    string
}

const (
	syncInterval = time.Minute * 1
)

func New(
	ctx context.Context,
	tracerProvider trace.TracerProvider,
	meterProvider metric.MeterProvider,
	db *db.DB,
	sqlcDB *sqlcdb.Client,
	edgePool *edge.Pool,
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
		buildCache:    buildCache,
		templateCache: templateCache,
		edgePool:      edgePool,

		lock:       sync.Mutex{},
		processing: make(map[uuid.UUID]processingBuilds),
	}

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
					err := tm.BuildStatusSync(ctx, b.EnvBuild.ID, b.Env.ID, utils.WithClusterFallback(b.Team.ClusterID), b.EnvBuild.ClusterNodeID)
					if err != nil {
						zap.L().Error("Error syncing build status", zap.Error(err), zap.String("buildID", b.EnvBuild.ID.String()))
					}
				}(b)
			}

			dbxCtxCancel()
		}
	}
}

func (tm *TemplateManager) GetAvailableBuildClient(ctx context.Context, clusterID uuid.UUID) (string, error) {
	cluster, ok := tm.edgePool.GetClusterById(clusterID)
	if !ok {
		return "", fmt.Errorf("cluster with ID '%s' not found", clusterID)
	}

	builder, err := cluster.GetAvailableTemplateBuilder(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get available template builder for cluster '%s': %w", clusterID, err)
	}

	return builder.NodeID, nil
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

func (tm *TemplateManager) DeleteBuild(ctx context.Context, buildID uuid.UUID, templateID string, clusterID uuid.UUID, nodeID string) error {
	ctx, span := tracer.Start(ctx, "delete-template",
		trace.WithAttributes(
			telemetry.WithBuildID(buildID.String()),
		),
	)
	defer span.End()

	client, err := tm.GetClusterBuildClient(clusterID, nodeID)
	if err != nil {
		// nodeID can be an orchestrator ID, if the build corresponds to a snapshot.
		// We may want to improve this later by adding the Delete method to Orchestrator as well.
		// This way we can remove the build (snapshot) from cache as well
		nodeID, err = tm.GetAvailableBuildClient(ctx, clusterID)
		if err != nil {
			return fmt.Errorf("failed to get any available node in the cluster: %w", err)
		}

		zap.L().Info("Fallback to available node", zap.String("nodeID", nodeID), zap.String("clusterID", clusterID.String()))
		client, err = tm.GetClusterBuildClient(clusterID, nodeID)
		if err != nil {
			return fmt.Errorf("failed to get builder client: %w", err)
		}
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
		err := tm.DeleteBuild(ctx, build.BuildID, build.TemplateID, build.ClusterID, build.NodeID)
		if err != nil {
			return fmt.Errorf("failed to delete env build '%s': %w", build.BuildID, err)
		}
	}

	return nil
}

func (tm *TemplateManager) GetStatus(ctx context.Context, buildID uuid.UUID, templateID string, clusterID uuid.UUID, nodeID string) (*templatemanagergrpc.TemplateBuildStatusResponse, error) {
	cli, err := tm.GetClusterBuildClient(clusterID, nodeID)
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
