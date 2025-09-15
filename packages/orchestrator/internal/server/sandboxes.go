package server

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/launchdarkly/go-sdk-common/v3/ldcontext"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	clickhouse "github.com/e2b-dev/infra/packages/clickhouse/pkg"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/events/event"
	featureflags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

var tracer = otel.Tracer("github.com/e2b-dev/infra/packages/orchestrator/internal/server")

const (
	requestTimeout              = 60 * time.Second
	maxStartingInstancesPerNode = 3
)

func (s *server) Create(ctx context.Context, req *orchestrator.SandboxCreateRequest) (*orchestrator.SandboxCreateResponse, error) {
	// set max request timeout for this request
	ctx, cancel := context.WithTimeoutCause(ctx, requestTimeout, fmt.Errorf("request timed out"))
	defer cancel()

	// set up tracing
	ctx, childSpan := tracer.Start(ctx, "sandbox-create")
	defer childSpan.End()

	childSpan.SetAttributes(
		telemetry.WithTemplateID(req.Sandbox.TemplateId),
		attribute.String("kernel.version", req.Sandbox.KernelVersion),
		telemetry.WithSandboxID(req.Sandbox.SandboxId),
		attribute.String("client.id", s.info.ClientId),
		attribute.String("envd.version", req.Sandbox.EnvdVersion),
	)

	// setup launch darkly
	ctx = featureflags.SetContext(
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

	maxRunningSandboxesPerNode, err := s.featureFlags.IntFlag(ctx, featureflags.MaxSandboxesPerNode)
	if err != nil {
		zap.L().Error("Failed to get MaxSandboxesPerNode flag", zap.Error(err))
	}

	runningSandboxes := s.sandboxes.Count()
	if runningSandboxes >= maxRunningSandboxesPerNode {
		telemetry.ReportEvent(ctx, "max number of running sandboxes reached")

		return nil, status.Errorf(codes.ResourceExhausted, "max number of running sandboxes on node reached (%d), please retry", maxRunningSandboxesPerNode)
	}

	// Check if we've reached the max number of starting instances on this node
	acquired := s.startingSandboxes.TryAcquire(1)
	if !acquired {
		telemetry.ReportEvent(ctx, "too many starting sandboxes on node")
		return nil, status.Errorf(codes.ResourceExhausted, "too many sandboxes starting on this node, please retry")
	}
	defer s.startingSandboxes.Release(1)

	metricsWriteFlag, flagErr := s.featureFlags.BoolFlag(ctx, featureflags.MetricsWriteFlagName)
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
	go func() {
		ctx, childSpan := tracer.Start(context.WithoutCancel(ctx), "sandbox-create-stop", trace.WithNewRoot())
		defer childSpan.End()

		waitErr := sbx.Wait(ctx)
		if waitErr != nil {
			sbxlogger.I(sbx).Error("failed to wait for sandbox, cleaning up", zap.Error(waitErr))
		}

		cleanupErr := sbx.Close(ctx)
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
	}()

	label := clickhouse.SandboxEventLabelCreate
	if req.Sandbox.Snapshot {
		label = clickhouse.SandboxEventLabelResume
	}

	teamID, buildId, eventData := s.prepareSandboxEventData(sbx)

	go s.sbxEventsService.HandleEvent(context.WithoutCancel(ctx), event.SandboxEvent{
		Timestamp:          time.Now().UTC(),
		SandboxID:          sbx.Runtime.SandboxID,
		SandboxExecutionID: sbx.Runtime.ExecutionID,
		SandboxTemplateID:  sbx.Config.BaseTemplateID,
		SandboxBuildID:     buildId,
		SandboxTeamID:      teamID,
		EventCategory:      string(clickhouse.SandboxEventCategoryLifecycle),
		EventLabel:         string(label),
		EventData:          eventData,
	})

	return &orchestrator.SandboxCreateResponse{
		ClientId: s.info.ClientId,
	}, nil
}

func (s *server) Update(ctx context.Context, req *orchestrator.SandboxUpdateRequest) (*emptypb.Empty, error) {
	ctx, childSpan := tracer.Start(ctx, "sandbox-update")
	defer childSpan.End()

	childSpan.SetAttributes(
		telemetry.WithSandboxID(req.SandboxId),
		attribute.String("client.id", s.info.ClientId),
	)

	sbx, ok := s.sandboxes.Get(req.SandboxId)
	if !ok {
		telemetry.ReportCriticalError(ctx, "sandbox not found", nil)

		return nil, status.Error(codes.NotFound, "sandbox not found")
	}

	sbx.EndAt = req.EndTime.AsTime()

	teamID, buildId, eventData := s.prepareSandboxEventData(sbx)
	eventData["set_timeout"] = req.EndTime.AsTime().Format(time.RFC3339)

	go s.sbxEventsService.HandleEvent(context.WithoutCancel(ctx), event.SandboxEvent{
		Timestamp:          time.Now().UTC(),
		SandboxID:          sbx.Runtime.SandboxID,
		SandboxExecutionID: sbx.Runtime.ExecutionID,
		SandboxTemplateID:  sbx.Config.BaseTemplateID,
		SandboxBuildID:     buildId,
		SandboxTeamID:      teamID,
		EventCategory:      string(clickhouse.SandboxEventCategoryLifecycle),
		EventLabel:         string(clickhouse.SandboxEventLabelUpdate),
		EventData:          eventData,
	})

	return &emptypb.Empty{}, nil
}

func (s *server) List(ctx context.Context, _ *emptypb.Empty) (*orchestrator.SandboxListResponse, error) {
	_, childSpan := tracer.Start(ctx, "sandbox-list")
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

	ctx, childSpan := tracer.Start(ctx, "sandbox-delete")
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
	sbx.Checks.Healthcheck(true) // nolint:contextcheck // TODO: fix this later

	// Start the cleanup in a goroutine—the initial kill request should be send as the first thing in stop, and at this point you cannot route to the sandbox anymore.
	// We don't wait for the whole cleanup to finish here.
	go func() {
		err := sbx.Stop(ctx)
		if err != nil {
			sbxlogger.I(sbx).Error("error stopping sandbox", logger.WithSandboxID(in.SandboxId), zap.Error(err))
		}
	}()

	teamID, buildId, eventData := s.prepareSandboxEventData(sbx)

	go s.sbxEventsService.HandleEvent(context.WithoutCancel(ctx), event.SandboxEvent{
		Timestamp:          time.Now().UTC(),
		SandboxID:          sbx.Runtime.SandboxID,
		SandboxExecutionID: sbx.Runtime.ExecutionID,
		SandboxTemplateID:  sbx.Config.BaseTemplateID,
		SandboxBuildID:     buildId,
		SandboxTeamID:      teamID,
		EventCategory:      string(clickhouse.SandboxEventCategoryLifecycle),
		EventLabel:         string(clickhouse.SandboxEventLabelKill),
		EventData:          eventData,
	})

	return &emptypb.Empty{}, nil
}

func (s *server) Pause(ctx context.Context, in *orchestrator.SandboxPauseRequest) (*emptypb.Empty, error) {
	ctx, childSpan := tracer.Start(ctx, "sandbox-pause")
	defer childSpan.End()

	// setup launch darkly
	ctx = featureflags.SetContext(
		ctx,
		ldcontext.NewBuilder(in.SandboxId).
			Kind(featureflags.SandboxKind).
			SetString(featureflags.SandboxTemplateAttribute, in.TemplateId).
			Build(),
	)

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
			ctx, childSpan := tracer.Start(ctx, "sandbox-pause-stop", trace.WithNewRoot())
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
	snapshot, err := sbx.Pause(ctx, meta)
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

	teamID, buildId, eventData := s.prepareSandboxEventData(sbx)

	go s.sbxEventsService.HandleEvent(context.WithoutCancel(ctx), event.SandboxEvent{
		Timestamp:          time.Now().UTC(),
		SandboxID:          sbx.Runtime.SandboxID,
		SandboxExecutionID: sbx.Runtime.ExecutionID,
		SandboxTemplateID:  sbx.Config.BaseTemplateID,
		SandboxBuildID:     buildId,
		SandboxTeamID:      teamID,
		EventCategory:      string(clickhouse.SandboxEventCategoryLifecycle),
		EventLabel:         string(clickhouse.SandboxEventLabelPause),
		EventData:          eventData,
	})

	return &emptypb.Empty{}, nil
}

// Extracts common data needed for sandbox events
func (s *server) prepareSandboxEventData(sbx *sandbox.Sandbox) (uuid.UUID, string, map[string]any) {
	teamID, err := uuid.Parse(sbx.Runtime.TeamID)
	if err != nil {
		sbxlogger.I(sbx).Error("error parsing team ID", zap.String("team_id", sbx.Runtime.TeamID), zap.Error(err))
	}

	buildId := ""
	eventData := make(map[string]any)
	if sbx.APIStoredConfig != nil {
		buildId = sbx.APIStoredConfig.BuildId
		if sbx.APIStoredConfig.Metadata != nil {
			// Copy the map to avoid race conditions
			eventData["sandbox_metadata"] = utils.ShallowCopyMap(sbx.APIStoredConfig.Metadata)
		}
	}

	return teamID, buildId, eventData
}
