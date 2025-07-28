package server

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	clickhouse "github.com/e2b-dev/infra/packages/clickhouse/pkg"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/config"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	featureflags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const (
	requestTimeout = 60 * time.Second
)

func (s *server) Create(ctxConn context.Context, req *orchestrator.SandboxCreateRequest) (*orchestrator.SandboxCreateResponse, error) {
	ctx, cancel := context.WithTimeoutCause(ctxConn, requestTimeout, fmt.Errorf("request timed out"))
	defer cancel()
	fmt.Println("~~~~~cREATE")

	childCtx, childSpan := s.tracer.Start(ctx, "sandbox-create")
	defer childSpan.End()

	childSpan.SetAttributes(
		telemetry.WithTemplateID(req.Sandbox.TemplateId),
		attribute.String("kernel.version", req.Sandbox.KernelVersion),
		telemetry.WithSandboxID(req.Sandbox.SandboxId),
		attribute.String("client.id", s.info.ClientId),
		attribute.String("envd.version", req.Sandbox.EnvdVersion),
	)

	// TODO: Temporary workaround, remove API changes deployed
	if req.Sandbox.GetExecutionId() == "" {
		req.Sandbox.ExecutionId = uuid.New().String()
	}

	metricsWriteFlag, flagErr := s.featureFlags.BoolFlag(featureflags.MetricsWriteFlagName, req.Sandbox.SandboxId)
	if flagErr != nil {
		zap.L().Error("soft failing during metrics write feature flag receive", zap.Error(flagErr))
	}

	template, err := s.templateCache.GetTemplate(
		req.Sandbox.TemplateId,
		req.Sandbox.BuildId,
		req.Sandbox.KernelVersion,
		req.Sandbox.FirecrackerVersion,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get template snapshot data: %w", err)
	}

	sbx, cleanup, err := sandbox.ResumeSandbox(
		childCtx,
		s.tracer,
		s.networkPool,
		template,
		req.Sandbox,
		childSpan.SpanContext().TraceID().String(),
		req.StartTime.AsTime(),
		req.EndTime.AsTime(),
		s.devicePool,
		config.AllowSandboxInternet,
		metricsWriteFlag,
	)
	if err != nil {
		zap.L().Error("failed to create sandbox, cleaning up", zap.Error(err))
		cleanupErr := cleanup.Run(ctx)

		err := errors.Join(err, context.Cause(ctx), cleanupErr)
		telemetry.ReportCriticalError(ctx, "failed to cleanup sandbox", err)

		return nil, status.Errorf(codes.Internal, "failed to cleanup sandbox: %s", err)
	}

	s.sandboxes.Insert(req.Sandbox.SandboxId, sbx)
	go func() {
		ctx, childSpan := s.tracer.Start(context.Background(), "sandbox-create-stop")
		defer childSpan.End()

		waitErr := sbx.Wait(ctx)
		if waitErr != nil {
			sbxlogger.I(sbx).Error("failed to wait for sandbox, cleaning up", zap.Error(waitErr))
		}

		cleanupErr := cleanup.Run(ctx)
		if cleanupErr != nil {
			sbxlogger.I(sbx).Error("failed to cleanup sandbox, will remove from cache", zap.Error(cleanupErr))
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

			return sbx.Config.ExecutionId == v.Config.ExecutionId
		})

		// Remove the proxies assigned to the sandbox from the pool to prevent them from being reused.
		s.proxy.RemoveFromPool(sbx.Config.ExecutionId)

		sbxlogger.E(sbx).Info("Sandbox killed")
	}()

	label := clickhouse.SandboxEventLabelResume
	if !req.Sandbox.Snapshot {
		label = clickhouse.SandboxEventLabelCreate
	}

	go func(label clickhouse.SandboxEventLabel) {
		err := s.clickhouseClient.InsertSandboxEvent(context.Background(), clickhouse.SandboxEvent{
			Timestamp:          time.Now().UTC(),
			SandboxID:          sbx.Config.SandboxId,
			SandboxTemplateID:  sbx.Config.TemplateId,
			SandboxTeamID:      sbx.Config.TeamId,
			SandboxExecutionID: sbx.Config.ExecutionId,
			EventCategory:      clickhouse.SandboxEventCategoryLifecycle,
			EventLabel:         label,
		})

		if err != nil {
			sbxlogger.I(sbx).Error("error inserting sandbox event during create", zap.Error(err))
		}
	}(label)

	return &orchestrator.SandboxCreateResponse{
		ClientId: s.info.ClientId,
	}, nil
}

func (s *server) Update(ctx context.Context, req *orchestrator.SandboxUpdateRequest) (*emptypb.Empty, error) {
	ctx, childSpan := s.tracer.Start(ctx, "sandbox-update")
	defer childSpan.End()

	childSpan.SetAttributes(
		telemetry.WithSandboxID(req.SandboxId),
		attribute.String("client.id", s.info.ClientId),
	)

	item, ok := s.sandboxes.Get(req.SandboxId)
	if !ok {
		telemetry.ReportCriticalError(ctx, "sandbox not found", nil)

		return nil, status.Error(codes.NotFound, "sandbox not found")
	}

	item.EndAt = req.EndTime.AsTime()

	go func() {
		err := s.clickhouseClient.InsertSandboxEvent(context.Background(), clickhouse.SandboxEvent{
			Timestamp:          time.Now().UTC(),
			SandboxID:          item.Config.SandboxId,
			SandboxTemplateID:  item.Config.TemplateId,
			SandboxTeamID:      item.Config.TeamId,
			SandboxExecutionID: item.Config.ExecutionId,
			EventCategory:      clickhouse.SandboxEventCategoryLifecycle,
			EventLabel:         clickhouse.SandboxEventLabelUpdate,
		})
		if err != nil {
			sbxlogger.I(item).Error("error inserting sandbox event during update", zap.Error(err))
		}
	}()

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
			ClientId:  s.info.ClientId,
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
		telemetry.WithSandboxID(in.SandboxId),
		attribute.String("client.id", s.info.ClientId),
	)

	sbx, ok := s.sandboxes.Get(in.SandboxId)
	if !ok {
		telemetry.ReportCriticalError(ctx, "sandbox not found", nil, telemetry.WithSandboxID(in.SandboxId))

		return nil, status.Errorf(codes.NotFound, "sandbox '%s' not found", in.SandboxId)
	}

	// Remove the sandbox from the cache to prevent loading it again in API during the time the instance is stopping.
	// Old comment:
	// 	Ensure the sandbox is removed from cache.
	// 	Ideally we would rely only on the goroutine defer.
	// Don't allow connecting to the sandbox anymore.
	s.sandboxes.Remove(in.SandboxId)

	// Check health metrics before stopping the sandbox
	sbx.Checks.Healthcheck(true)

	// Start the cleanup in a goroutine—the initial kill request should be send as the first thing in stop, and at this point you cannot route to the sandbox anymore.
	// We don't wait for the whole cleanup to finish here.
	go func() {
		err := sbx.Stop(ctx)
		if err != nil {
			sbxlogger.I(sbx).Error("error stopping sandbox", logger.WithSandboxID(in.SandboxId), zap.Error(err))
		}
	}()

	go func() {
		err := s.clickhouseClient.InsertSandboxEvent(context.Background(), clickhouse.SandboxEvent{
			Timestamp:          time.Now().UTC(),
			SandboxID:          sbx.Config.SandboxId,
			SandboxTemplateID:  sbx.Config.TemplateId,
			SandboxTeamID:      sbx.Config.TeamId,
			SandboxExecutionID: sbx.Config.ExecutionId,
			EventCategory:      clickhouse.SandboxEventCategoryLifecycle,
			EventLabel:         clickhouse.SandboxEventLabelKill,
		})
		if err != nil {
			sbxlogger.I(sbx).Error("error inserting sandbox event during kill", zap.Error(err))
		}
	}()

	return &emptypb.Empty{}, nil
}

func (s *server) Pause(ctx context.Context, in *orchestrator.SandboxPauseRequest) (*emptypb.Empty, error) {
	ctx, childSpan := s.tracer.Start(ctx, "sandbox-pause")
	defer childSpan.End()

	s.pauseMu.Lock()

	sbx, ok := s.sandboxes.Get(in.SandboxId)
	if !ok {
		s.pauseMu.Unlock()

		telemetry.ReportCriticalError(ctx, "sandbox not found", nil)

		return nil, status.Error(codes.NotFound, "sandbox not found")
	}

	s.sandboxes.Remove(in.SandboxId)

	s.pauseMu.Unlock()

	snapshotTemplateFiles, err := storage.TemplateFiles{
		TemplateID:         in.TemplateId,
		BuildID:            in.BuildId,
		KernelVersion:      sbx.Config.KernelVersion,
		FirecrackerVersion: sbx.Config.FirecrackerVersion,
	}.CacheFiles()
	if err != nil {
		telemetry.ReportCriticalError(ctx, "error creating template files", err)

		return nil, status.Errorf(codes.Internal, "error creating template files: %s", err)
	}

	defer func() {
		// sbx.Stop sometimes blocks for several seconds,
		// so we don't want to block the request and do the cleanup in a goroutine after we already removed sandbox from cache and proxy.
		go func() {
			ctx, childSpan := s.tracer.Start(context.Background(), "sandbox-pause-stop")
			defer childSpan.End()

			err := sbx.Stop(ctx)
			if err != nil {
				sbxlogger.I(sbx).Error("error stopping sandbox after snapshot", logger.WithSandboxID(in.SandboxId), zap.Error(err))
			}
		}()
	}()

	snapshot, err := sbx.Pause(ctx, s.tracer, snapshotTemplateFiles)
	if err != nil {
		telemetry.ReportCriticalError(ctx, "error snapshotting sandbox", err, telemetry.WithSandboxID(in.SandboxId))

		return nil, status.Errorf(codes.Internal, "error snapshotting sandbox '%s': %s", in.SandboxId, err)
	}

	err = s.templateCache.AddSnapshot(
		snapshotTemplateFiles.TemplateID,
		snapshotTemplateFiles.BuildID,
		snapshotTemplateFiles.KernelVersion,
		snapshotTemplateFiles.FirecrackerVersion,
		snapshot.MemfileDiffHeader,
		snapshot.RootfsDiffHeader,
		snapshot.Snapfile,
		snapshot.MemfileDiff,
		snapshot.RootfsDiff,
	)
	if err != nil {
		telemetry.ReportCriticalError(ctx, "error adding snapshot to template cache", err)

		return nil, status.Errorf(codes.Internal, "error adding snapshot to template cache: %s", err)
	}

	telemetry.ReportEvent(ctx, "added snapshot to template cache")

	go func() {
		err := snapshot.Upload(context.Background(), s.persistence, snapshotTemplateFiles.TemplateFiles)
		if err != nil {
			sbxlogger.I(sbx).Error("error uploading sandbox snapshot", zap.Error(err))

			return
		}
	}()

	go func() {
		err := s.clickhouseClient.InsertSandboxEvent(context.Background(), clickhouse.SandboxEvent{
			Timestamp:          time.Now().UTC(),
			SandboxID:          sbx.Config.SandboxId,
			SandboxTemplateID:  sbx.Config.TemplateId,
			SandboxTeamID:      sbx.Config.TeamId,
			SandboxExecutionID: sbx.Config.ExecutionId,
			EventCategory:      clickhouse.SandboxEventCategoryLifecycle,
			EventLabel:         clickhouse.SandboxEventLabelPause,
		})
		if err != nil {
			sbxlogger.I(sbx).Error("error inserting sandbox event during pause", zap.Error(err))
		}
	}()

	return &emptypb.Empty{}, nil
}
