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
	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
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

	lock       sync.Mutex
	tracer     trace.Tracer
	processing map[uuid.UUID]processingBuilds
	buildCache *templatecache.TemplatesBuildCache
	lokiClient *loki.DefaultClient
	sqlcDB     *sqlcdb.Client

	localClient       *grpclient.GRPCClient
	localClientMutex  sync.RWMutex
	localClientStatus infogrpc.ServiceInfoStatus
}

type DeleteBuild struct {
	BuildID    uuid.UUID
	TemplateId string

	ClusterId     *uuid.UUID
	ClusterNodeId *string
}

const (
	syncInterval = time.Minute * 1
)

var ErrLocalTemplateManagerNotAvailable = errors.New("local template manager is not available")

func New(ctx context.Context, tracer trace.Tracer, tracerProvider trace.TracerProvider, meterProvider metric.MeterProvider, db *db.DB, sqlcDB *sqlcdb.Client, edgePool *edge.Pool, lokiClient *loki.DefaultClient, buildCache *templatecache.TemplatesBuildCache) (*TemplateManager, error) {
	client, err := createClient(tracerProvider, meterProvider)
	if err != nil {
		return nil, fmt.Errorf("failed to establish GRPC connection: %w", err)
	}

	tm := &TemplateManager{
		grpc:       client,
		db:         db,
		sqlcDB:     sqlcDB,
		tracer:     tracer,
		buildCache: buildCache,
		edgePool:   edgePool,
		lokiClient: lokiClient,

		localClient:       client,
		localClientMutex:  sync.RWMutex{},
		localClientStatus: infogrpc.ServiceInfoStatus_OrchestratorHealthy,

		lock:       sync.Mutex{},
		processing: make(map[uuid.UUID]processingBuilds),
	}

	// periodically check for local template manager health status
	go tm.localBuilderHealthCheckSync(ctx)

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

func (tm *TemplateManager) getBuilderClient(clusterID *uuid.UUID, nodeID *string, placement bool) (*grpclient.GRPCClient, metadata.MD, PlacementLogsProvider, error) {
	if clusterID == nil || nodeID == nil {
		tm.localClientMutex.RLock()

		// build placement requires healthy template builder
		if placement && tm.GetLocalClientStatus() != infogrpc.ServiceInfoStatus_OrchestratorHealthy {
			tm.localClientMutex.RUnlock()
			zap.L().Error("Local template manager is not fully healthy, cannot use it for placement new builds")
			return nil, nil, nil, ErrLocalTemplateManagerNotAvailable
		}

		// for getting build information only not valid state is getting already unhealthy builder
		if tm.GetLocalClientStatus() == infogrpc.ServiceInfoStatus_OrchestratorUnhealthy {
			tm.localClientMutex.RUnlock()
			zap.L().Error("Local template manager is unhealthy")
			return nil, nil, nil, ErrLocalTemplateManagerNotAvailable
		}

		tm.localClientMutex.RUnlock()
		meta := metadata.New(map[string]string{})
		logs := NewLokiPlacementLogsProvider(tm.lokiClient)
		return tm.grpc, meta, logs, nil
	}

	cluster, ok := tm.edgePool.GetClusterById(*clusterID)
	if !ok {
		return nil, nil, nil, errors.New("cluster not found")
	}

	node, err := cluster.GetTemplateBuilderById(*nodeID)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to get builder by id '%s': %w", *nodeID, err)
	}

	logs := NewClusterPlacementLogsProvider(cluster.GetHttpClient(), *nodeID)
	client, clientMetadata := cluster.GetGrpcClient(node.NodeID)

	return client, clientMetadata, logs, nil
}

func (tm *TemplateManager) DeleteBuild(ctx context.Context, t trace.Tracer, buildID uuid.UUID, templateID string, clusterID *uuid.UUID, clusterNodeID *string) error {
	ctx, span := t.Start(ctx, "delete-template",
		trace.WithAttributes(
			telemetry.WithBuildID(buildID.String()),
		),
	)
	defer span.End()

	client, clientMd, _, err := tm.getBuilderClient(clusterID, clusterNodeID, false)
	if err != nil {
		return fmt.Errorf("failed to get builder edgeHttpClient: %w", err)
	}

	reqCtx := metadata.NewOutgoingContext(ctx, clientMd)
	_, err = client.Template.TemplateBuildDelete(
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
		err := tm.DeleteBuild(ctx, tm.tracer, build.BuildID, build.TemplateId, build.ClusterId, build.ClusterNodeId)
		if err != nil {
			return fmt.Errorf("failed to delete env build '%s': %w", build.BuildID, err)
		}
	}

	return nil
}

func (tm *TemplateManager) CreateTemplate(t trace.Tracer, ctx context.Context, templateID string, buildID uuid.UUID, kernelVersion, firecrackerVersion, startCommand string, vCpuCount, diskSizeMB, memoryMB int64, readyCommand string, clusterID *uuid.UUID, clusterNodeID *string) error {
	ctx, span := t.Start(ctx, "create-template",
		trace.WithAttributes(
			telemetry.WithTemplateID(templateID),
		),
	)
	defer span.End()

	features, err := sandbox.NewVersionInfo(firecrackerVersion)
	if err != nil {
		return fmt.Errorf("failed to get features for firecracker version '%s': %w", firecrackerVersion, err)
	}

	client, clientMd, _, err := tm.getBuilderClient(clusterID, clusterNodeID, true)
	if err != nil {
		return fmt.Errorf("failed to get builder edgeHttpClient: %w", err)
	}

	reqCtx := metadata.NewOutgoingContext(ctx, clientMd)
	_, err = client.Template.TemplateCreate(
		reqCtx, &templatemanagergrpc.TemplateCreateRequest{
			Template: &templatemanagergrpc.TemplateConfig{
				TemplateID:         templateID,
				BuildID:            buildID.String(),
				VCpuCount:          int32(vCpuCount),
				MemoryMB:           int32(memoryMB),
				DiskSizeMB:         int32(diskSizeMB),
				KernelVersion:      kernelVersion,
				FirecrackerVersion: firecrackerVersion,
				HugePages:          features.HasHugePages(),
				StartCommand:       startCommand,
				ReadyCommand:       readyCommand,
			},
		},
	)

	err = utils.UnwrapGRPCError(err)
	if err != nil {
		return fmt.Errorf("failed to create template '%s': %w", templateID, err)
	}

	telemetry.ReportEvent(ctx, "Template build started")
	return nil
}

func (tm *TemplateManager) GetStatus(ctx context.Context, buildID uuid.UUID, templateID string, clusterID *uuid.UUID, clusterNodeID *string) (*templatemanagergrpc.TemplateBuildStatusResponse, error) {
	client, clientMd, _, err := tm.getBuilderClient(clusterID, clusterNodeID, false)
	if err != nil {
		return nil, fmt.Errorf("failed to get builder edgeHttpClient: %w", err)
	}

	reqCtx := metadata.NewOutgoingContext(ctx, clientMd)

	// error unwrapping is done in the caller
	return client.Template.TemplateBuildStatus(
		reqCtx,
		&templatemanagergrpc.TemplateStatusRequest{
			BuildID: buildID.String(), TemplateID: templateID,
		},
	)
}

func (tm *TemplateManager) GetLogs(ctx context.Context, buildID uuid.UUID, templateID string, clusterID *uuid.UUID, clusterNodeID *string, offset *int32) ([]string, error) {
	ctx, span := tm.tracer.Start(ctx, "get-build-logs",
		trace.WithAttributes(
			telemetry.WithTemplateID(templateID),
			telemetry.WithBuildID(buildID.String()),
		),
	)
	defer span.End()

	_, _, logs, err := tm.getBuilderClient(clusterID, clusterNodeID, false)
	if err != nil {
		emptyLogs := make([]string, 0)
		return emptyLogs, fmt.Errorf("failed to get builder edgeHttpClient: %w", err)
	}

	return logs.GetLogs(ctx, buildID.String(), templateID, offset)
}
