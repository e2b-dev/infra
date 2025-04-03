package server

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"
	"golang.org/x/sync/semaphore"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/pkg/database"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/build"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const (
	requestTimeout = 60 * time.Second

	maxParalellSnapshotting = 8
)

func (s *server) Create(ctxConn context.Context, req *orchestrator.SandboxCreateRequest) (*orchestrator.SandboxCreateResponse, error) {
	ctx, cancel := context.WithTimeoutCause(ctxConn, requestTimeout, fmt.Errorf("request timed out"))
	defer cancel()

	childCtx, childSpan := s.tracer.Start(ctx, "sandbox-create")
	defer childSpan.End()

	childSpan.SetAttributes(
		attribute.String("template.id", req.Sandbox.TemplateId),
		attribute.String("kernel.version", req.Sandbox.KernelVersion),
		attribute.String("sandbox.id", req.Sandbox.SandboxId),
		attribute.String("client.id", s.clientID),
		attribute.String("envd.version", req.Sandbox.EnvdVersion),
	)

	sbx, cleanup, err := sandbox.NewSandbox(
		childCtx,
		s.tracer,
		s.dns,
		s.proxy,
		s.networkPool,
		s.templateCache,
		req.Sandbox,
		childSpan.SpanContext().TraceID().String(),
		req.StartTime.AsTime(),
		req.EndTime.AsTime(),
		req.Sandbox.Snapshot,
		req.Sandbox.BaseTemplateId,
		s.clientID,
		s.devicePool,
		s.clickhouseStore,
		s.useLokiMetrics,
		s.useClickhouseMetrics,
	)
	if err != nil {
		zap.L().Error("failed to create sandbox, cleaning up", zap.Error(err))

		cleanupErr := cleanup.Run(ctx)

		err = status.Errorf(codes.Internal, "failed to create sandbox: %v", errors.Join(err, context.Cause(ctx), cleanupErr))
		telemetry.ReportCriticalError(ctx, err)

		return nil, err
	}

	started, err := s.recordSandboxStart(ctx, sbx)
	if err != nil {
		zap.L().Error("failed to register sandbox, cleaning up", zap.Error(err))

		cleanupErr := cleanup.Run(ctx)

		err = status.Errorf(codes.Internal, "failed to register sandbox: %v", errors.Join(err, context.Cause(ctx), cleanupErr))
		telemetry.ReportCriticalError(ctx, err)

		return nil, err
	}

	go func() {
		ctx, childSpan := s.tracer.Start(context.Background(), "sandbox-create-stop")
		defer childSpan.End()

		if err := sbx.Wait(ctx); err != nil {
			sbxlogger.I(sbx).Error("failed to wait for sandbox, cleaning up", zap.Error(err))
		}

		ended := time.Now()

		if err := cleanup.Run(ctx); err != nil {
			sbxlogger.I(sbx).Error("failed to cleanup sandbox, will remove from cache", zap.Error(err))
		}

		// Remove the sandbox from cache only if the cleanup IDs match.
		// This prevents us from accidentally removing started sandbox (via resume) from the cache if cleanup is taking longer than the request timeout.
		// This could have caused the "invisible" sandboxes that are not in orchestrator or API, but are still on client.
		s.sandboxes.RemoveCb(req.Sandbox.SandboxId, func(_ string, v *sandbox.Sandbox, exists bool) bool {
			if !exists {
				return false
			}

			if v == nil {
				return false
			}

			return sbx.CleanupID == v.CleanupID
		})

		if err := s.db.SetSandboxTerminated(ctx, req.Sandbox.SandboxId, ended.Sub(started)); err != nil {
			sbxlogger.I(sbx).Error("failed to cleanup db record for sandbox", zap.Error(err))
		}

		sbxlogger.E(sbx).Info("sandbox killed")
	}()

	return &orchestrator.SandboxCreateResponse{
		ClientId: s.clientID,
	}, nil
}

func (s *server) recordSandboxStart(ctx context.Context, sbx *sandbox.Sandbox) (time.Time, error) {
	started := time.Now().UTC()
	s.sandboxes.Insert(sbx.Config.SandboxId, sbx)

	// TODO: this could become JSON (pro: easier to read with
	// external tools/code, con: larger size and slower
	// serialization.)
	confProto, err := proto.Marshal(sbx.Config)
	if err != nil {
		// TODO: decide if we want to return early and abort in this case.
		sbxlogger.I(sbx).Error("failed to marshal sandbox config for the database", zap.Error(err))
	}

	if err := s.db.CreateSandbox(ctx, database.CreateSandboxParams{
		ID:        sbx.Config.SandboxId,
		Status:    database.SandboxStatusRunning,
		StartedAt: started,
		Deadline:  sbx.EndAt,
		Config:    confProto,
	}); err != nil {
		return started, err
	}

	return started, nil
}

func (s *server) Update(ctx context.Context, req *orchestrator.SandboxUpdateRequest) (*emptypb.Empty, error) {
	ctx, childSpan := s.tracer.Start(ctx, "sandbox-update")
	defer childSpan.End()

	childSpan.SetAttributes(
		attribute.String("sandbox.id", req.SandboxId),
		attribute.String("client.id", s.clientID),
	)

	item, ok := s.sandboxes.Get(req.SandboxId)
	if !ok {
		err := status.Errorf(codes.NotFound, "sandbox not found")
		telemetry.ReportCriticalError(ctx, err)
		return nil, err
	}

	item.EndAt = req.EndTime.AsTime()
	if err := s.db.UpdateSandboxDeadline(ctx, req.SandboxId, item.EndAt); err != nil {
		return nil, status.Errorf(codes.Internal, "db update sandbox: %v", err)
	}

	return &emptypb.Empty{}, nil
}

func (s *server) List(ctx context.Context, _ *emptypb.Empty) (*orchestrator.SandboxListResponse, error) {
	_, childSpan := s.tracer.Start(ctx, "sandbox-list")
	defer childSpan.End()

	items := s.sandboxes.Items()

	sandboxes := make([]*orchestrator.RunningSandbox, 0, len(items))

	for _, sbx := range items {
		if sbx == nil {
			continue
		}

		if sbx.Config == nil {
			continue
		}

		sandboxes = append(sandboxes, &orchestrator.RunningSandbox{
			Config:    sbx.Config,
			ClientId:  s.clientID,
			StartTime: timestamppb.New(sbx.StartedAt),
			EndTime:   timestamppb.New(sbx.EndAt),
		})
	}

	return &orchestrator.SandboxListResponse{
		Sandboxes: sandboxes,
	}, nil
}

func (s *server) Delete(ctxConn context.Context, in *orchestrator.SandboxDeleteRequest) (*emptypb.Empty, error) {
	ctx, cancel := context.WithTimeoutCause(ctxConn, requestTimeout, fmt.Errorf("request timed out"))
	defer cancel()

	ctx, childSpan := s.tracer.Start(ctx, "sandbox-delete")
	defer childSpan.End()

	childSpan.SetAttributes(
		attribute.String("sandbox.id", in.SandboxId),
		attribute.String("client.id", s.clientID),
	)

	sbx, ok := s.sandboxes.Get(in.SandboxId)
	if !ok {
		err := fmt.Errorf("sandbox '%s' not found", in.SandboxId)
		telemetry.ReportCriticalError(ctx, err)

		return nil, status.Errorf(codes.NotFound, err.Error())
	}

	// Don't allow connecting to the sandbox anymore.
	s.dns.Remove(in.SandboxId, sbx.Slot.HostIP())
	s.proxy.RemoveSandbox(in.SandboxId, sbx.Slot.HostIP())

	// Remove the sandbox from the cache to prevent loading it again in API during the time the instance is stopping.
	// Old comment:
	// 	Ensure the sandbox is removed from cache.
	// 	Ideally we would rely only on the goroutine defer.
	s.sandboxes.Remove(in.SandboxId)

	loggingCtx, cancelLogginCtx := context.WithTimeout(ctx, 2*time.Second)
	defer cancelLogginCtx()

	// Check health metrics before stopping the sandbox
	sbx.Healthcheck(loggingCtx, true)
	sbx.LogMetrics(loggingCtx)

	err := sbx.Stop(ctx)
	if err != nil {
		sbxlogger.I(sbx).Error("error stopping sandbox", zap.String("sandbox_id", in.SandboxId), zap.Error(err))
	}

	if err := s.db.SetSandboxTerminated(ctx, in.SandboxId, sbx.EndAt.Sub(sbx.StartedAt)); err != nil {
		sbxlogger.I(sbx).Error("error setting sandbox deleted", zap.String("sandbox_id", in.SandboxId), zap.Error(err))
	}

	return &emptypb.Empty{}, nil
}

var pauseQueue = semaphore.NewWeighted(maxParalellSnapshotting)

func (s *server) Pause(ctx context.Context, in *orchestrator.SandboxPauseRequest) (*emptypb.Empty, error) {
	ctx, childSpan := s.tracer.Start(ctx, "sandbox-pause")
	defer childSpan.End()

	err := pauseQueue.Acquire(ctx, 1)
	if err != nil {
		telemetry.ReportCriticalError(ctx, err)

		return nil, status.Errorf(codes.ResourceExhausted, err.Error())
	}

	releaseOnce := sync.OnceFunc(func() {
		pauseQueue.Release(1)
	})

	defer releaseOnce()

	s.pauseMu.Lock()
	sbx, ok := s.sandboxes.Get(in.SandboxId)
	if !ok {
		s.pauseMu.Unlock()

		err := fmt.Errorf("sandbox not found")
		telemetry.ReportCriticalError(ctx, err)

		return nil, status.Errorf(codes.NotFound, err.Error())
	}

	s.dns.Remove(in.SandboxId, sbx.Slot.HostIP())
	s.sandboxes.Remove(in.SandboxId)

	if err := s.db.SetSandboxPaused(ctx, in.SandboxId, time.Now().Sub(sbx.StartedAt)); err != nil {
		sbxlogger.I(sbx).Error("error setting sandbox deleted", zap.String("sandbox_id", in.SandboxId), zap.Error(err))
	}

	s.pauseMu.Unlock()

	snapshotTemplateFiles, err := storage.NewTemplateFiles(
		in.TemplateId,
		in.BuildId,
		sbx.Config.KernelVersion,
		sbx.Config.FirecrackerVersion,
		sbx.Config.HugePages,
	).NewTemplateCacheFiles()
	if err != nil {
		err = fmt.Errorf("error creating template files: %w", err)
		telemetry.ReportCriticalError(ctx, err)

		return nil, status.Errorf(codes.Internal, err.Error())
	}

	defer func() {
		// sbx.Stop sometimes blocks for several seconds,
		// so we don't want to block the request and do the cleanup in a goroutine after we already removed sandbox from cache and DNS.
		go func() {
			ctx, childSpan := s.tracer.Start(context.Background(), "sandbox-pause-stop")
			defer childSpan.End()

			if err := sbx.Stop(ctx); err != nil {
				sbxlogger.I(sbx).Error("error stopping sandbox after snapshot", zap.String("sandbox_id", in.SandboxId), zap.Error(err))
			}
		}()
	}()

	err = os.MkdirAll(snapshotTemplateFiles.CacheDir(), 0o755)
	if err != nil {
		err = fmt.Errorf("error creating sandbox cache dir '%s': %w", snapshotTemplateFiles.CacheDir(), err)
		telemetry.ReportCriticalError(ctx, err)

		return nil, status.Errorf(codes.Internal, err.Error())
	}

	snapshot, err := sbx.Snapshot(ctx, s.tracer, snapshotTemplateFiles, releaseOnce)
	if err != nil {
		err = fmt.Errorf("error snapshotting sandbox '%s': %w", in.SandboxId, err)
		telemetry.ReportCriticalError(ctx, err)

		return nil, status.Errorf(codes.Internal, err.Error())
	}

	err = s.templateCache.AddSnapshot(
		snapshotTemplateFiles.TemplateId,
		snapshotTemplateFiles.BuildId,
		snapshotTemplateFiles.KernelVersion,
		snapshotTemplateFiles.FirecrackerVersion,
		snapshotTemplateFiles.Hugepages(),
		snapshot.MemfileDiffHeader,
		snapshot.RootfsDiffHeader,
		snapshot.Snapfile,
		snapshot.MemfileDiff,
		snapshot.RootfsDiff,
	)
	if err != nil {
		err = fmt.Errorf("error adding snapshot to template cache: %w", err)
		telemetry.ReportCriticalError(ctx, err)

		return nil, status.Errorf(codes.Internal, err.Error())
	}

	telemetry.ReportEvent(ctx, "added snapshot to template cache")

	go func() {
		var memfilePath *string

		switch r := snapshot.MemfileDiff.(type) {
		case *build.NoDiff:
			break
		default:
			memfileLocalPath, err := r.CachePath()
			if err != nil {
				sbxlogger.I(sbx).Error("error getting memfile diff path", zap.Error(err))

				return
			}

			memfilePath = &memfileLocalPath
		}

		var rootfsPath *string

		switch r := snapshot.RootfsDiff.(type) {
		case *build.NoDiff:
			break
		default:
			rootfsLocalPath, err := r.CachePath()
			if err != nil {
				sbxlogger.I(sbx).Error("error getting rootfs diff path", zap.Error(err))

				return
			}

			rootfsPath = &rootfsLocalPath
		}

		b := storage.NewTemplateBuild(
			snapshot.MemfileDiffHeader,
			snapshot.RootfsDiffHeader,
			snapshotTemplateFiles.TemplateFiles,
		)

		err = <-b.Upload(
			context.Background(),
			snapshotTemplateFiles.CacheSnapfilePath(),
			memfilePath,
			rootfsPath,
		)
		if err != nil {
			sbxlogger.I(sbx).Error("error uploading sandbox snapshot", zap.Error(err))

			return
		}
	}()

	return &emptypb.Empty{}, nil
}

func (s *server) StatusReport(ctx context.Context, _ *emptypb.Empty) (*orchestrator.OrchestratorStatus, error) {
	report, err := s.db.Status(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	return &orchestrator.OrchestratorStatus{
		GlobalVersion:                     report.GlobalVersion,
		RunningSandboxes:                  report.RunningSandboxes,
		PendingSandboxes:                  report.PendingSandboxes,
		TerminatedSandboxes:               report.TerminatedSandboxes,
		NumSandboxes:                      report.NumSandboxes,
		Status:                            report.Status,
		UpdatedAt:                         timestamppb.New(report.UpdatedAt),
		EarliestRunningSandboxStartedAt:   timestamppb.New(report.OldestSandboxStartTime()),
		MostRecentRunningSandboxUpdatedAt: timestamppb.New(report.MostRecentSandboxModification()),
	}, nil
}
