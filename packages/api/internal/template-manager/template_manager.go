package template_manager

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"
	loki "github.com/grafana/loki/pkg/logcli/client"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/metadata"

	templatecache "github.com/e2b-dev/infra/packages/api/internal/cache/templates"
	"github.com/e2b-dev/infra/packages/api/internal/edge"
	grpclient "github.com/e2b-dev/infra/packages/api/internal/grpc"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	sqlcdb "github.com/e2b-dev/infra/packages/db/client"
	"github.com/e2b-dev/infra/packages/shared/pkg/db"
	infogrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator-info"
	tempaltemanagergrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
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
	syncInterval             = time.Minute * 1
	syncTimeout              = time.Minute * 15
	syncWaitingStateDeadline = time.Minute * 40
)

var (
	templateManagerHost = os.Getenv("TEMPLATE_MANAGER_HOST")

	ErrLocalTemplateManagerNotAvailable = errors.New("local template manager is not available")
)

func New(ctx context.Context, tracer trace.Tracer, tracerProvider trace.TracerProvider, meterProvider metric.MeterProvider, db *db.DB, sqlcDB *sqlcdb.Client, edgePool *edge.Pool, lokiClient *loki.DefaultClient, buildCache *templatecache.TemplatesBuildCache) (*TemplateManager, error) {
	conn, err := grpc.NewClient(templateManagerHost,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithStatsHandler(
			otelgrpc.NewClientHandler(
				otelgrpc.WithTracerProvider(tracerProvider),
				otelgrpc.WithMeterProvider(meterProvider),
			),
		),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                10 * time.Second, // Send ping every 10s
			Timeout:             2 * time.Second,  // Wait 2s for response
			PermitWithoutStream: true,
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to establish GRPC connection: %w", err)
	}

	client := &grpclient.GRPCClient{
		Sandbox:    nil,
		Info:       infogrpc.NewInfoServiceClient(conn),
		Template:   tempaltemanagergrpc.NewTemplateServiceClient(conn),
		Connection: conn,
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
	// todo: we should track all in-progress requests and wait for them to finish
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
				go tm.BuildStatusSync(ctx, b.EnvBuild.ID, b.Env.ID, b.Team.ClusterID, b.EnvBuild.ClusterNodeID)
			}

			dbxCtxCancel()
		}
	}
}

func (tm *TemplateManager) getBuilderClient(clusterId *uuid.UUID, nodeId *string, placement bool) (*grpclient.GRPCClient, metadata.MD, PlacementLogsProvider, error) {
	if clusterId == nil || nodeId == nil {
		tm.localClientMutex.RLock()

		// build placement requires healthy template builder
		if placement && tm.localClientStatus != infogrpc.ServiceInfoStatus_OrchestratorHealthy {
			tm.localClientMutex.RUnlock()
			zap.L().Error("Local template manager is not fully healthy, cannot use it for placement new builds")
			return nil, nil, nil, ErrLocalTemplateManagerNotAvailable
		}

		// for getting build information only not valid state is getting already unhealthy builder
		if tm.localClientStatus == infogrpc.ServiceInfoStatus_OrchestratorUnhealthy {
			tm.localClientMutex.RUnlock()
			zap.L().Error("Local template manager is unhealthy")
			return nil, nil, nil, ErrLocalTemplateManagerNotAvailable
		}

		tm.localClientMutex.RUnlock()
		meta := metadata.New(map[string]string{})
		logs := NewLokiPlacementLogsProvider(tm.lokiClient)
		return tm.grpc, meta, logs, nil
	}

	cluster, ok := tm.edgePool.GetClusterById(*clusterId)
	if !ok {
		return nil, nil, nil, errors.New("cluster not found")
	}

	node, err := cluster.GetTemplateBuilderById(*nodeId)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to get builder by id '%s': %w", *nodeId, err)
	}

	logs := NewClusterPlacementLogsProvider(cluster.GetHttpClient(), *nodeId)
	client, clientMetadata := cluster.GetGrpcClient(node.Id)

	return client, clientMetadata, logs, nil
}

func (tm *TemplateManager) DeleteBuild(ctx context.Context, t trace.Tracer, buildId uuid.UUID, templateId string, clusterId *uuid.UUID, clusterNodeId *string) error {
	ctx, span := t.Start(ctx, "delete-template",
		trace.WithAttributes(
			telemetry.WithBuildID(buildId.String()),
		),
	)
	defer span.End()

	client, clientMd, _, err := tm.getBuilderClient(clusterId, clusterNodeId, false)
	if err != nil {
		return fmt.Errorf("failed to get builder client: %w", err)
	}

	reqCtx := metadata.NewOutgoingContext(ctx, clientMd)
	_, err = client.Template.TemplateBuildDelete(
		reqCtx, &tempaltemanagergrpc.TemplateBuildDeleteRequest{
			BuildID:    buildId.String(),
			TemplateID: templateId,
		},
	)

	err = utils.UnwrapGRPCError(err)
	if err != nil {
		return fmt.Errorf("failed to delete env build '%s': %w", buildId, err)
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

func (tm *TemplateManager) CreateTemplate(t trace.Tracer, ctx context.Context, templateID string, buildID uuid.UUID, kernelVersion, firecrackerVersion, startCommand string, vCpuCount, diskSizeMB, memoryMB int64, readyCommand string, clusterId *uuid.UUID, clusterNodeId *string) error {
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

	client, clientMd, _, err := tm.getBuilderClient(clusterId, clusterNodeId, true)
	if err != nil {
		return fmt.Errorf("failed to get builder client: %w", err)
	}

	reqCtx := metadata.NewOutgoingContext(ctx, clientMd)
	_, err = client.Template.TemplateCreate(
		reqCtx, &tempaltemanagergrpc.TemplateCreateRequest{
			Template: &tempaltemanagergrpc.TemplateConfig{
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

func (tm *TemplateManager) GetStatus(ctx context.Context, buildId uuid.UUID, templateId string, clusterId *uuid.UUID, clusterNodeId *string) (*tempaltemanagergrpc.TemplateBuildStatusResponse, error) {
	client, clientMd, _, err := tm.getBuilderClient(clusterId, clusterNodeId, false)
	if err != nil {
		return nil, fmt.Errorf("failed to get builder client: %w", err)
	}

	reqCtx := metadata.NewOutgoingContext(ctx, clientMd)

	// error unwrapping is done in the caller
	return client.Template.TemplateBuildStatus(
		reqCtx,
		&tempaltemanagergrpc.TemplateStatusRequest{
			BuildID: buildId.String(), TemplateID: templateId,
		},
	)
}

func (tm *TemplateManager) GetLogs(ctx context.Context, buildId uuid.UUID, templateId string, clusterId *uuid.UUID, clusterNodeId *string, offset *int32) (*[]string, error) {
	ctx, span := tm.tracer.Start(ctx, "get-build-logs",
		trace.WithAttributes(
			telemetry.WithTemplateID(templateId),
			telemetry.WithBuildID(buildId.String()),
		),
	)
	defer span.End()

	_, _, logs, err := tm.getBuilderClient(clusterId, clusterNodeId, false)
	if err != nil {
		emptyLogs := make([]string, 0)
		return &emptyLogs, fmt.Errorf("failed to get builder client: %w", err)
	}

	return logs.GetLogs(ctx, buildId.String(), templateId, offset)
}
