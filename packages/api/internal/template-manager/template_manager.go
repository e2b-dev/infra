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
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"google.golang.org/grpc/metadata"

	templatecache "github.com/e2b-dev/infra/packages/api/internal/cache/templates"
	"github.com/e2b-dev/infra/packages/api/internal/edge"
	"github.com/e2b-dev/infra/packages/api/internal/orchestrator"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	sqlcdb "github.com/e2b-dev/infra/packages/db/client"
	"github.com/e2b-dev/infra/packages/shared/pkg/db"
	template_manager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

type processingBuilds struct {
	templateID string
}

type TemplateManager struct {
	grpc     *orchestrator.GRPCClient
	edgePool *edge.Pool
	db       *db.DB

	lock       sync.Mutex
	tracer     trace.Tracer
	processing map[uuid.UUID]processingBuilds
	buildCache *templatecache.TemplatesBuildCache
	lokiClient *loki.DefaultClient
	sqlcDB     *sqlcdb.Client
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

func New(ctx context.Context, tracer trace.Tracer, tracerProvider trace.TracerProvider, meterProvider metric.MeterProvider, db *db.DB, sqlcDB *sqlcdb.Client, edgePool *edge.Pool, lokiClient *loki.DefaultClient, buildCache *templatecache.TemplatesBuildCache) (*TemplateManager, error) {
	// todo
	host := os.Getenv("TEMPLATE_MANAGER_ADDRESS")
	client, err := orchestrator.NewClient(tracerProvider, meterProvider, host, false)
	if err != nil {
		return nil, err
	}

	return &TemplateManager{
		grpc:       client,
		db:         db,
		sqlcDB:     sqlcDB,
		tracer:     tracer,
		buildCache: buildCache,
		edgePool:   edgePool,
		lokiClient: lokiClient,

		lock:       sync.Mutex{},
		processing: make(map[uuid.UUID]processingBuilds),
	}, nil
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

func (tm *TemplateManager) getPlacement(clusterId *uuid.UUID, nodeId *string) (*BuildPlacement, error) {
	if clusterId == nil || nodeId == nil {
		// todo: implement
		//if !tm.grpc.IsReadyForBuildPlacement() {
		//	return nil, fmt.Errorf("local template manager is not ready for build placement")
		//}

		logsProvider := NewLokiPlacementLogsProvider(tm.lokiClient)
		meta := metadata.New(map[string]string{})

		return NewBuildPlacement(tm.grpc, meta, logsProvider), nil
	}

	// todo: we should ideally check if builder node is health and ready for builds
	cluster, ok := tm.edgePool.GetClusterById(*clusterId)
	if !ok {
		return nil, errors.New("cluster not found")
	}

	// todo: ideally make this somehow private?
	clientMetadata := cluster.GetGrpcClientMetadata(*nodeId)
	client := cluster.GetGrpcClient()

	httpClient := cluster.GetHttpClient()
	logsProvider := NewClusterPlacementLogsProvider(httpClient, *nodeId)
	return NewBuildPlacement(client, clientMetadata, logsProvider), nil
}

func (tm *TemplateManager) DeleteBuild(ctx context.Context, t trace.Tracer, buildId uuid.UUID, templateId string, clusterId *uuid.UUID, clusterNodeId *string) error {
	ctx, span := t.Start(ctx, "delete-template",
		trace.WithAttributes(
			telemetry.WithBuildID(buildId.String()),
		),
	)
	defer span.End()

	buildPlacement, err := tm.getPlacement(clusterId, clusterNodeId)
	if err != nil {
		return err
	}

	return buildPlacement.DeleteBuild(ctx, buildId.String(), templateId)
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

	buildPlacement, err := tm.getPlacement(clusterId, clusterNodeId)
	if err != nil {
		return err
	}

	err = buildPlacement.StartBuild(
		ctx,
		&template_manager.TemplateCreateRequest{
			Template: &template_manager.TemplateConfig{
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
	if err != nil {
		return err
	}

	telemetry.ReportEvent(ctx, "Template build started")
	return nil
}

func (tm *TemplateManager) GetLogs(ctx context.Context, buildId uuid.UUID, templateId string, clusterId *uuid.UUID, clusterNodeId *string, offset *int32) (*[]string, error) {
	ctx, span := tm.tracer.Start(ctx, "get-build-logs",
		trace.WithAttributes(
			telemetry.WithTemplateID(templateId),
			telemetry.WithBuildID(buildId.String()),
		),
	)
	defer span.End()

	buildPlacement, err := tm.getPlacement(clusterId, clusterNodeId)
	if err != nil {
		return nil, err
	}

	return buildPlacement.GetLogs(ctx, buildId.String(), templateId, offset)
}
