package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/launchdarkly/go-sdk-common/v3/ldcontext"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	clickhouse "github.com/e2b-dev/infra/packages/clickhouse/pkg"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	featureflags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const requestTimeout = 60 * time.Second

func (s *server) Create(ctx context.Context, req *orchestrator.SandboxCreateRequest) (*orchestrator.SandboxCreateResponse, error) {
	// set max request timeout for this request
	ctx, cancel := context.WithTimeoutCause(ctx, requestTimeout, fmt.Errorf("request timed out"))
	defer cancel()

	// set up tracing
	ctx, childSpan := s.tracer.Start(ctx, "sandbox-create")
	defer childSpan.End()

	childSpan.SetAttributes(
		telemetry.WithTemplateID(req.Sandbox.TemplateId),
		attribute.String("kernel.version", req.Sandbox.KernelVersion),
		telemetry.WithSandboxID(req.Sandbox.SandboxId),
		attribute.String("client.id", s.info.ClientId),
		attribute.String("envd.version", req.Sandbox.EnvdVersion),
	)

	// setup launch darkly
	ctx = featureflags.CreateContext(
		ctx,
		ldcontext.NewBuilder(req.Sandbox.SandboxId).
			Kind(featureflags.SandboxKind).
			SetString(featureflags.SandboxTemplateAttribute, req.Sandbox.TemplateId).
			SetString(featureflags.SandboxKernelVersionAttribute, req.Sandbox.KernelVersion).
			SetString(featureflags.SandboxFirecrackerVersionAttribute, req.Sandbox.FirecrackerVersion).
			Build(),
		ldcontext.NewBuilder(req.Sandbox.TeamId).
			Kind(featureflags.TeamKind).
			Build(),
	)

	metricsWriteFlag, flagErr := s.featureFlags.BoolFlag(ctx, featureflags.MetricsWriteFlagName, req.Sandbox.SandboxId)
	if flagErr != nil {
		zap.L().Error("soft failing during metrics write feature flag receive", zap.Error(flagErr))
	}

	template, err := s.templateCache.GetTemplate(
		ctx,
		req.GetSandbox().GetBuildId(),
		req.GetSandbox().GetKernelVersion(),
		req.GetSandbox().GetFirecrackerVersion(),
		req.GetSandbox().GetSnapshot(),
		false,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get template snapshot data: %w", err)
	}

	sbx, err := sandbox.ResumeSandbox(
		ctx,
		s.tracer,
		s.networkPool,
		template,
		sandbox.Config{
			BaseTemplateID: req.Sandbox.BaseTemplateId,

			Vcpu:            req.Sandbox.Vcpu,
			RamMB:           req.Sandbox.RamMb,
			TotalDiskSizeMB: req.Sandbox.TotalDiskSizeMb,
			HugePages:       req.Sandbox.HugePages,

			AllowInternetAccess: req.Sandbox.AllowInternetAccess,

			Envd: sandbox.EnvdMetadata{
				Version:     req.Sandbox.EnvdVersion,
				AccessToken: req.Sandbox.EnvdAccessToken,
				Vars:        req.Sandbox.EnvVars,
			},
		},
		sandbox.RuntimeMetadata{
			TemplateID:  req.Sandbox.TemplateId,
			SandboxID:   req.Sandbox.SandboxId,
			ExecutionID: req.Sandbox.ExecutionId,
			TeamID:      req.Sandbox.TeamId,
		},
		childSpan.SpanContext().TraceID().String(),
		req.StartTime.AsTime(),
		req.EndTime.AsTime(),
		s.devicePool,
		metricsWriteFlag,
		req.Sandbox,
	)
	if err != nil {
		err := errors.Join(err, context.Cause(ctx))
		telemetry.ReportCriticalError(ctx, "failed to create sandbox", err)
		return nil, status.Errorf(codes.Internal, "failed to create sandbox: %s", err)
	}

	s.sandboxes.Insert(req.Sandbox.SandboxId, sbx)
	go func(ctx context.Context) {
		ctx, childSpan := s.tracer.Start(ctx, "sandbox-create-stop", trace.WithNewRoot())
		defer childSpan.End()

		waitErr := sbx.Wait(ctx)
		if waitErr != nil {
			sbxlogger.I(sbx).Error("failed to wait for sandbox, cleaning up", zap.Error(waitErr))
		}

		cleanupErr := sbx.Stop(ctx)
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

			return sbx.Runtime.ExecutionID == v.Runtime.ExecutionID
		})

		// Remove the proxies assigned to the sandbox from the pool to prevent them from being reused.
		s.proxy.RemoveFromPool(sbx.Runtime.ExecutionID)

		sbxlogger.E(sbx).Info("Sandbox killed")
	}(context.WithoutCancel(ctx))

	label := clickhouse.SandboxEventLabelCreate
	if req.Sandbox.Snapshot {
		label = clickhouse.SandboxEventLabelResume
	}

	sandboxLifeCycleEventsWriteFlag, flagErr := s.featureFlags.BoolFlag(ctx,
		featureflags.SandboxLifeCycleEventsWriteFlagName, req.Sandbox.SandboxId)
	if flagErr != nil {
		zap.L().Error("soft failing during sandbox lifecycle events write feature flag receive", zap.Error(flagErr))
	}
	if sandboxLifeCycleEventsWriteFlag {
		go func(label clickhouse.SandboxEventLabel) {
			buildId := ""
			if sbx.APIStoredConfig != nil {
				buildId = sbx.APIStoredConfig.BuildId
			}

			teamID, err := uuid.Parse(sbx.Runtime.TeamID)
			if err != nil {
				sbxlogger.I(sbx).Error("error parsing team ID", zap.String("team_id", sbx.Runtime.TeamID), zap.Error(err))
				return
			}

			err = s.sandboxEventBatcher.Push(clickhouse.SandboxEvent{
				Timestamp:          time.Now().UTC(),
				SandboxID:          sbx.Runtime.SandboxID,
				SandboxTemplateID:  sbx.Config.BaseTemplateID,
				SandboxBuildID:     buildId,
				SandboxTeamID:      teamID,
				SandboxExecutionID: sbx.Runtime.ExecutionID,
				EventCategory:      string(clickhouse.SandboxEventCategoryLifecycle),
				EventLabel:         string(label),
				EventData:          sql.NullString{String: "", Valid: false},
			})
			if err != nil {
				sbxlogger.I(sbx).Error(
					"error inserting sandbox lifecycle event", zap.String("event_label", string(label)), zap.Error(err))
			}
		}(label)
	}

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

	// TODO: adapt to new types of update events
	eventData := fmt.Sprintf(`{"set_timeout": "%s"}`, req.EndTime.AsTime().Format(time.RFC3339))

	sandboxLifeCycleEventsWriteFlag, flagErr := s.featureFlags.BoolFlag(ctx,
		featureflags.SandboxLifeCycleEventsWriteFlagName, item.Runtime.SandboxID)
	if flagErr != nil {
		zap.L().Error("soft failing during sandbox lifecycle events write feature flag receive", zap.Error(flagErr))
	}
	if sandboxLifeCycleEventsWriteFlag {
		go func(eventData string) {
			buildId := ""
			if item.APIStoredConfig != nil {
				buildId = item.APIStoredConfig.BuildId
			}

			teamID, err := uuid.Parse(item.Runtime.TeamID)
			if err != nil {
				sbxlogger.I(item).Error("error parsing team ID", zap.String("team_id", item.Runtime.TeamID), zap.Error(err))
				return
			}

			err = s.sandboxEventBatcher.Push(clickhouse.SandboxEvent{
				Timestamp:          time.Now().UTC(),
				SandboxID:          item.Runtime.SandboxID,
				SandboxTemplateID:  item.Config.BaseTemplateID,
				SandboxBuildID:     buildId,
				SandboxTeamID:      teamID,
				SandboxExecutionID: item.Runtime.ExecutionID,
				EventCategory:      string(clickhouse.SandboxEventCategoryLifecycle),
				EventLabel:         string(clickhouse.SandboxEventLabelUpdate),
				EventData:          sql.NullString{String: eventData, Valid: true},
			})
			if err != nil {
				sbxlogger.I(item).Error(
					"error inserting sandbox lifecycle event", zap.String("event_label", string(clickhouse.SandboxEventLabelUpdate)), zap.Error(err))
			}
		}(eventData)
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

		if sbx.APIStoredConfig == nil {
			continue
		}

		sandboxes = append(sandboxes, &orchestrator.RunningSandbox{
			Config:    sbx.APIStoredConfig,
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

	sandboxLifeCycleEventsWriteFlag, flagErr := s.featureFlags.BoolFlag(ctx,
		featureflags.SandboxLifeCycleEventsWriteFlagName, sbx.Runtime.SandboxID)
	if flagErr != nil {
		zap.L().Error("soft failing during sandbox lifecycle events write feature flag receive", zap.Error(flagErr))
	}
	if sandboxLifeCycleEventsWriteFlag {
		go func() {
			buildId := ""
			if sbx.APIStoredConfig != nil {
				buildId = sbx.APIStoredConfig.BuildId
			}

			teamID, err := uuid.Parse(sbx.Runtime.TeamID)
			if err != nil {
				sbxlogger.I(sbx).Error("error parsing team ID", zap.String("team_id", sbx.Runtime.TeamID), zap.Error(err))
				return
			}

			err = s.sandboxEventBatcher.Push(clickhouse.SandboxEvent{
				Timestamp:          time.Now().UTC(),
				SandboxID:          sbx.Runtime.SandboxID,
				SandboxTemplateID:  sbx.Config.BaseTemplateID,
				SandboxBuildID:     buildId,
				SandboxTeamID:      teamID,
				SandboxExecutionID: sbx.Runtime.ExecutionID,
				EventCategory:      string(clickhouse.SandboxEventCategoryLifecycle),
				EventLabel:         string(clickhouse.SandboxEventLabelKill),
				EventData:          sql.NullString{String: "", Valid: false},
			})
			if err != nil {
				sbxlogger.I(sbx).Error(
					"error inserting sandbox lifecycle event", zap.String("event_label", string(clickhouse.SandboxEventLabelKill)), zap.Error(err))
			}
		}()
	}

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

	defer func(ctx context.Context) {
		// sbx.Stop sometimes blocks for several seconds,
		// so we don't want to block the request and do the cleanup in a goroutine after we already removed sandbox from cache and proxy.
		go func() {
			ctx, childSpan := s.tracer.Start(ctx, "sandbox-pause-stop")
			defer childSpan.End()

			err := sbx.Stop(ctx)
			if err != nil {
				sbxlogger.I(sbx).Error("error stopping sandbox after snapshot", logger.WithSandboxID(in.SandboxId), zap.Error(err))
			}
		}()
	}(context.WithoutCancel(ctx))

	meta, err := sbx.Template.Metadata()
	if err != nil {
		return nil, fmt.Errorf("no metadata found in template: %w", err)
	}

	fcVersions := sbx.FirecrackerVersions()
	meta = meta.SameVersionTemplate(storage.TemplateFiles{
		BuildID:            in.BuildId,
		KernelVersion:      fcVersions.KernelVersion,
		FirecrackerVersion: fcVersions.FirecrackerVersion,
	})
	snapshot, err := sbx.Pause(ctx, s.tracer, meta)
	if err != nil {
		telemetry.ReportCriticalError(ctx, "error snapshotting sandbox", err, telemetry.WithSandboxID(in.SandboxId))

		return nil, status.Errorf(codes.Internal, "error snapshotting sandbox '%s': %s", in.SandboxId, err)
	}

	err = s.templateCache.AddSnapshot(
		ctx,
		meta.Template.BuildID,
		meta.Template.KernelVersion,
		meta.Template.FirecrackerVersion,
		snapshot.MemfileDiffHeader,
		snapshot.RootfsDiffHeader,
		snapshot.Snapfile,
		snapshot.Metafile,
		snapshot.MemfileDiff,
		snapshot.RootfsDiff,
	)
	if err != nil {
		telemetry.ReportCriticalError(ctx, "error adding snapshot to template cache", err)

		return nil, status.Errorf(codes.Internal, "error adding snapshot to template cache: %s", err)
	}

	telemetry.ReportEvent(ctx, "added snapshot to template cache")

	go func(ctx context.Context) {
		err := snapshot.Upload(ctx, s.persistence, meta.Template)
		if err != nil {
			sbxlogger.I(sbx).Error("error uploading sandbox snapshot", zap.Error(err))

			return
		}
	}(context.WithoutCancel(ctx))

	sandboxLifeCycleEventsWriteFlag, flagErr := s.featureFlags.BoolFlag(ctx,
		featureflags.SandboxLifeCycleEventsWriteFlagName, sbx.Runtime.SandboxID)
	if flagErr != nil {
		zap.L().Error("soft failing during sandbox lifecycle events write feature flag receive", zap.Error(flagErr))
	}

	if sandboxLifeCycleEventsWriteFlag {
		go func() {
			buildId := ""
			if sbx.APIStoredConfig != nil {
				buildId = sbx.APIStoredConfig.BuildId
			}

			teamID, err := uuid.Parse(sbx.Runtime.TeamID)
			if err != nil {
				sbxlogger.I(sbx).Error("error parsing team ID", zap.String("team_id", sbx.Runtime.TeamID), zap.Error(err))
				return
			}

			err = s.sandboxEventBatcher.Push(clickhouse.SandboxEvent{
				Timestamp:          time.Now().UTC(),
				SandboxID:          sbx.Runtime.SandboxID,
				SandboxTemplateID:  sbx.Config.BaseTemplateID,
				SandboxBuildID:     buildId,
				SandboxTeamID:      teamID,
				SandboxExecutionID: sbx.Runtime.ExecutionID,
				EventCategory:      string(clickhouse.SandboxEventCategoryLifecycle),
				EventLabel:         string(clickhouse.SandboxEventLabelPause),
				EventData:          sql.NullString{String: "", Valid: false},
			})
			if err != nil {
				sbxlogger.I(sbx).Error(
					"error inserting sandbox lifecycle event", zap.String("event_label", string(clickhouse.SandboxEventLabelPause)), zap.Error(err))
			}
		}()
	}

	return &emptypb.Empty{}, nil
}
