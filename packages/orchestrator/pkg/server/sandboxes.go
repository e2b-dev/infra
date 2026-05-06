package server

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jellydator/ttlcache/v3"
	"github.com/launchdarkly/go-sdk-common/v3/ldcontext"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/fc"
	sbxtemplate "github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/template"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/template/metadata"
	"github.com/e2b-dev/infra/packages/shared/pkg/events"
	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
	sandbox_network "github.com/e2b-dev/infra/packages/shared/pkg/sandbox-network"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

var tracer = otel.Tracer("github.com/e2b-dev/infra/packages/orchestrator/pkg/server")

const (
	requestTimeout = 60 * time.Second
	// acquireTimeout is the max time to wait for a semaphore for resuming sandboxes snapshot.
	acquireTimeout              = 15 * time.Second
	maxStartingInstancesPerNode = 3

	// uploadTimeout is the max time allowed for uploading snapshot files to
	// remote storage.
	uploadTimeout = 20 * time.Minute
	// redisPeerKeyTTL is slightly longer than uploadTimeout so the key is still
	// valid for the entire upload window before being cleaned up.
	redisPeerKeyTTL = uploadTimeout + 2*time.Minute

	// executionEventDataKey is the key used in webhook event data for sandbox execution metrics.
	executionEventDataKey = "execution"
)

func (s *Server) Create(ctx context.Context, req *orchestrator.SandboxCreateRequest) (_ *orchestrator.SandboxCreateResponse, createErr error) {
	// set max request timeout for this request
	ctx, cancel := context.WithTimeoutCause(ctx, requestTimeout, errors.New("request timed out"))
	defer cancel()

	// set up tracing
	ctx, childSpan := tracer.Start(ctx, "sandbox-create")
	defer childSpan.End()

	isResume := req.GetSandbox().GetSnapshot()
	createStart := time.Now()
	defer func() {
		if createErr != nil {
			return
		}

		s.sandboxCreateDuration.Record(ctx, time.Since(createStart).Milliseconds(),
			metric.WithAttributes(
				attribute.Bool("sandbox.resume", isResume),
			),
		)
	}()

	childSpan.SetAttributes(
		telemetry.WithBuildID(req.GetSandbox().GetBuildId()),
		telemetry.WithTeamID(req.GetSandbox().GetTeamId()),
		telemetry.WithTemplateID(req.GetSandbox().GetTemplateId()),
		telemetry.WithKernelVersion(req.GetSandbox().GetKernelVersion()),
		telemetry.WithSandboxID(req.GetSandbox().GetSandboxId()),
		telemetry.WithEnvdVersion(req.GetSandbox().GetEnvdVersion()),
	)

	// setup launch darkly
	ctx = featureflags.AddToContext(
		ctx,
		ldcontext.NewBuilder(req.GetSandbox().GetSandboxId()).
			Kind(featureflags.SandboxKind).
			SetString(featureflags.SandboxTemplateAttribute, req.GetSandbox().GetTemplateId()).
			SetString(featureflags.SandboxKernelVersionAttribute, req.GetSandbox().GetKernelVersion()).
			SetString(featureflags.SandboxFirecrackerVersionAttribute, req.GetSandbox().GetFirecrackerVersion()).
			Build(),
		ldcontext.NewBuilder(req.GetSandbox().GetTeamId()).
			Kind(featureflags.TeamKind).
			Build(),
		featureflags.VersionContext(s.info.ClientId, s.info.SourceCommit),
	)

	maxRunningSandboxesPerNode := s.featureFlags.IntFlag(ctx, featureflags.MaxSandboxesPerNode)

	runningSandboxes := s.sandboxFactory.Sandboxes.Count()
	if runningSandboxes >= maxRunningSandboxesPerNode {
		telemetry.ReportEvent(ctx, "max number of running sandboxes reached")

		return nil, status.Errorf(codes.ResourceExhausted, "max number of running sandboxes on node reached (%d), please retry", maxRunningSandboxesPerNode)
	}

	// Check if we've reached the max number of starting instances on this node
	if req.GetSandbox().GetSnapshot() {
		err := s.waitForAcquire(ctx)
		if err != nil {
			return nil, err
		}
	} else {
		acquired := s.startingSandboxes.TryAcquire(1)
		if !acquired {
			telemetry.ReportEvent(ctx, "too many starting sandboxes on node")

			return nil, status.Errorf(codes.ResourceExhausted, "too many sandboxes starting on this node, please retry")
		}
	}
	defer s.startingSandboxes.Release(1)

	template, err := s.templateCache.GetTemplate(
		ctx,
		req.GetSandbox().GetBuildId(),
		req.GetSandbox().GetSnapshot(),
		false,
		sbxtemplate.GetTemplateOpts{MaxSandboxLengthHours: req.GetSandbox().GetMaxSandboxLength()},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get template snapshot data: %w", err)
	}

	// Clone the network config to avoid modifying the original request
	network := proto.CloneOf(req.GetSandbox().GetNetwork())

	// TODO: Temporarily set this based on global config, should be removed later
	// https://linear.app/e2b/issue/ENG-3291
	//  (it should be passed network config from API)
	allowInternet := s.config.AllowSandboxInternet
	if req.GetSandbox().AllowInternetAccess != nil {
		allowInternet = req.GetSandbox().GetAllowInternetAccess()
	}
	if !allowInternet {
		if network == nil {
			network = &orchestrator.SandboxNetworkConfig{}
		}
		if network.GetEgress() == nil {
			network.Egress = &orchestrator.SandboxNetworkEgressConfig{}
		}
		network.Egress.DeniedCidrs = []string{sandbox_network.AllInternetTrafficCIDR}
	}

	resolvedFCVersion := featureflags.ResolveFirecrackerVersion(ctx, s.featureFlags, req.GetSandbox().GetFirecrackerVersion())
	volumeMounts, err := createVolumeMountModelsFromAPI(req.GetSandbox().GetVolumeMounts())
	if err != nil {
		return nil, fmt.Errorf("failed to convert volume mounts: %w", err)
	}

	config := sandbox.NewConfig(sandbox.Config{
		BaseTemplateID: req.GetSandbox().GetBaseTemplateId(),

		Vcpu:            req.GetSandbox().GetVcpu(),
		RamMB:           req.GetSandbox().GetRamMb(),
		TotalDiskSizeMB: req.GetSandbox().GetTotalDiskSizeMb(),
		HugePages:       req.GetSandbox().GetHugePages(),

		Network: network,

		Envd: sandbox.EnvdMetadata{
			Version:     req.GetSandbox().GetEnvdVersion(),
			AccessToken: req.GetSandbox().EnvdAccessToken,
			Vars:        req.GetSandbox().GetEnvVars(),
		},

		FirecrackerConfig: fc.Config{
			KernelVersion:      req.GetSandbox().GetKernelVersion(),
			FirecrackerVersion: resolvedFCVersion,
		},

		VolumeMounts:          volumeMounts,
		MaxSandboxLengthHours: req.GetSandbox().GetMaxSandboxLength(),
	})
	childSpan.SetAttributes(
		telemetry.WithFirecrackerVersion(config.FirecrackerConfig.FirecrackerVersion),
	)

	runtime := sandbox.RuntimeMetadata{
		TemplateID:  req.GetSandbox().GetTemplateId(),
		SandboxID:   req.GetSandbox().GetSandboxId(),
		ExecutionID: req.GetSandbox().GetExecutionId(),
		TeamID:      req.GetSandbox().GetTeamId(),
		BuildID:     req.GetSandbox().GetBuildId(),
		SandboxType: sandbox.SandboxTypeSandbox,
	}

	sbx, err := s.sandboxFactory.ResumeSandbox(
		ctx,
		template,
		config,
		runtime,
		req.GetStartTime().AsTime(),
		req.GetEndTime().AsTime(),
		req.GetSandbox(),
	)
	if err != nil {
		if errors.Is(err, storage.ErrObjectNotExist) {
			// Snapshot data not found, let the API know the data aren't probably upload yet
			telemetry.ReportError(ctx, "sandbox files not found", err, telemetry.WithSandboxID(req.GetSandbox().GetSandboxId()))

			return nil, status.Errorf(codes.FailedPrecondition, "sandbox files for '%s' not found", req.GetSandbox().GetSandboxId())
		}

		err = errors.Join(err, context.Cause(ctx))
		telemetry.ReportCriticalError(ctx, "failed to create sandbox", err)
		logger.L().Error(ctx, "failed to create sandbox", zap.Error(err),
			logger.WithSandboxID(runtime.SandboxID),
			logger.WithBuildID(runtime.BuildID),
			logger.WithTemplateID(runtime.TemplateID),
			logger.WithEnvdVersion(config.Envd.Version),
			logger.WithKernelVersion(config.FirecrackerConfig.KernelVersion),
			logger.WithFirecrackerVersion(config.FirecrackerConfig.FirecrackerVersion),
		)

		return nil, status.Errorf(codes.Internal, "failed to create sandbox: %s", err)
	}

	s.setupSandboxLifecycle(ctx, sbx)

	eventType := events.SandboxCreatedEventPair
	if req.GetSandbox().GetSnapshot() {
		eventType = events.SandboxResumedEventPair
	}

	teamID, buildId, eventData := s.prepareSandboxEventData(ctx, sbx)
	go s.sbxEventsService.Publish(
		context.WithoutCancel(ctx),
		teamID,
		events.SandboxEvent{
			Version:   events.StructureVersionV2,
			ID:        uuid.New(),
			Type:      eventType.Type,
			Timestamp: time.Now().UTC(),

			EventData:          eventData,
			SandboxID:          sbx.Runtime.SandboxID,
			SandboxExecutionID: sbx.Runtime.ExecutionID,
			SandboxTemplateID:  sbx.Config.BaseTemplateID,
			SandboxBuildID:     buildId,
			SandboxTeamID:      teamID,
		},
	)

	return &orchestrator.SandboxCreateResponse{
		ClientId: s.info.ClientId,
	}, nil
}

func createVolumeMountModelsFromAPI(volumeMounts []*orchestrator.SandboxVolumeMount) ([]sandbox.VolumeMountConfig, error) {
	var errs []error

	results := make([]sandbox.VolumeMountConfig, 0, len(volumeMounts))

	for _, v := range volumeMounts {
		volumeID, err := uuid.Parse(v.GetId())
		if err != nil {
			errs = append(errs, fmt.Errorf("invalid volume id %q: %w", v.GetId(), err))

			continue
		}

		results = append(results, sandbox.VolumeMountConfig{
			ID:   volumeID,
			Name: v.GetName(),
			Path: v.GetPath(),
			Type: v.GetType(),
		})
	}

	return results, errors.Join(errs...)
}

func (s *Server) Update(ctx context.Context, req *orchestrator.SandboxUpdateRequest) (*emptypb.Empty, error) {
	ctx, childSpan := tracer.Start(ctx, "sandbox-update")
	defer childSpan.End()

	childSpan.SetAttributes(
		telemetry.WithSandboxID(req.GetSandboxId()),
		attribute.String("client.id", s.info.ClientId),
	)

	sbx, ok := s.sandboxFactory.Sandboxes.Get(req.GetSandboxId())
	if !ok {
		telemetry.ReportCriticalError(ctx, "sandbox not found", nil)

		return nil, status.Error(codes.NotFound, "sandbox not found")
	}

	var updates []utils.UpdateFunc

	if req.GetEndTime() != nil {
		updates = append(updates, func(_ context.Context) (func(context.Context), error) {
			old := sbx.GetEndAt()
			sbx.SetEndAt(req.GetEndTime().AsTime())

			return func(_ context.Context) { sbx.SetEndAt(old) }, nil
		})
	}

	if req.GetEgress() != nil {
		updates = append(updates, func(ctx context.Context) (func(context.Context), error) {
			oldEgress := sbx.Config.GetNetworkEgress()

			if err := sbx.Slot.UpdateInternet(ctx, req.GetEgress()); err != nil {
				return nil, fmt.Errorf("failed to update sandbox network: %w", err)
			}

			egress := req.GetEgress()
			if len(egress.GetAllowedCidrs()) == 0 && len(egress.GetDeniedCidrs()) == 0 && len(egress.GetAllowedDomains()) == 0 && len(egress.GetRules()) == 0 {
				sbx.Config.SetNetworkEgress(nil)
			} else {
				sbx.Config.SetNetworkEgress(egress)
			}

			return func(ctx context.Context) {
				_ = sbx.Slot.UpdateInternet(ctx, oldEgress)
				sbx.Config.SetNetworkEgress(oldEgress)
			}, nil
		})
	}

	if err := utils.ApplyAllOrNone(ctx, updates); err != nil {
		telemetry.ReportCriticalError(ctx, "failed to update sandbox", err)

		return nil, status.Errorf(codes.Internal, "failed to update sandbox: %s", err)
	}

	// Publish event if any updates were applied.
	if len(updates) > 0 {
		teamID, buildId, eventData := s.prepareSandboxEventData(ctx, sbx)
		if req.GetEndTime() != nil {
			eventData["set_timeout"] = req.GetEndTime().AsTime().Format(time.RFC3339)
		}
		if egress := req.GetEgress(); egress != nil {
			eventData["network_egress"] = map[string]any{
				"allowed_cidrs":   egress.GetAllowedCidrs(),
				"denied_cidrs":    egress.GetDeniedCidrs(),
				"allowed_domains": egress.GetAllowedDomains(),
			}
		}

		go s.sbxEventsService.Publish(
			context.WithoutCancel(ctx),
			teamID,
			events.SandboxEvent{
				Version:   events.StructureVersionV2,
				ID:        uuid.New(),
				Type:      events.SandboxUpdatedEventPair.Type,
				Timestamp: time.Now().UTC(),

				EventData:          eventData,
				SandboxID:          sbx.Runtime.SandboxID,
				SandboxExecutionID: sbx.Runtime.ExecutionID,
				SandboxTemplateID:  sbx.Config.BaseTemplateID,
				SandboxBuildID:     buildId,
				SandboxTeamID:      teamID,
			},
		)
	}

	return &emptypb.Empty{}, nil
}

func (s *Server) List(ctx context.Context, _ *emptypb.Empty) (*orchestrator.SandboxListResponse, error) {
	_, childSpan := tracer.Start(ctx, "sandbox-list")
	defer childSpan.End()

	items := s.sandboxFactory.Sandboxes.Items()

	sandboxes := make([]*orchestrator.RunningSandbox, 0, len(items))

	for _, sbx := range items {
		if sbx == nil {
			continue
		}

		if sbx.APIStoredConfig == nil {
			continue
		}

		startedAt := sbx.GetStartedAt()
		sandboxes = append(sandboxes, &orchestrator.RunningSandbox{
			Config:    sbx.APIStoredConfig,
			ClientId:  s.info.ClientId,
			StartTime: timestamppb.New(startedAt),
			EndTime:   timestamppb.New(sbx.GetEndAt()),
		})
	}

	return &orchestrator.SandboxListResponse{
		Sandboxes: sandboxes,
	}, nil
}

func (s *Server) Delete(ctxConn context.Context, in *orchestrator.SandboxDeleteRequest) (*emptypb.Empty, error) {
	ctx, cancel := context.WithTimeoutCause(ctxConn, requestTimeout, errors.New("request timed out"))
	defer cancel()

	ctx, childSpan := tracer.Start(ctx, "sandbox-delete")
	defer childSpan.End()

	childSpan.SetAttributes(
		telemetry.WithSandboxID(in.GetSandboxId()),
		attribute.String("client.id", s.info.ClientId),
	)

	sbx, ok := s.sandboxFactory.Sandboxes.Get(in.GetSandboxId())
	if !ok {
		telemetry.ReportCriticalError(ctx, "sandbox not found", nil, telemetry.WithSandboxID(in.GetSandboxId()))

		return nil, status.Errorf(codes.NotFound, "sandbox '%s' not found", in.GetSandboxId())
	}

	// Mark the sandbox as stopping so it is excluded from live queries (Get, Items,
	// Count) but remains findable by IP (GetByHostPort) while the Firecracker
	// process finishes shutting down.
	// This prevents the sandbox from being synced to API again.
	marked := s.sandboxFactory.Sandboxes.MarkStopping(ctx, sbx.Runtime.SandboxID, sbx.LifecycleID)
	if !marked {
		telemetry.ReportCriticalError(ctx, "failed to mark sandbox as stopping", nil, telemetry.WithSandboxID(in.GetSandboxId()))

		return nil, status.Errorf(codes.Internal, "failed to delete sandbox '%s'", in.GetSandboxId())
	}

	sbxlogger.E(sbx).Info(ctx, "Killing sandbox")

	// Check health metrics before stopping the sandbox
	sbx.Checks.Healthcheck(ctx, true)

	// Start the cleanup in a goroutine—the initial kill request should be send as the first thing in stop, and at this point you cannot route to the sandbox anymore.
	// We don't wait for the whole cleanup to finish here.
	go func() {
		err := sbx.Stop(context.WithoutCancel(ctx))
		if err != nil {
			sbxlogger.I(sbx).Error(ctx, "error stopping sandbox", logger.WithSandboxID(in.GetSandboxId()), zap.Error(err))
		}
	}()

	teamID, buildId, eventData := s.prepareSandboxEventData(ctx, sbx)
	if s.featureFlags.BoolFlag(ctx, featureflags.ExecutionMetricsOnWebhooksFlag) {
		eventData[executionEventDataKey] = s.getSandboxExecutionData(sbx)
	}

	eventType := events.SandboxKilledEventPair
	go s.sbxEventsService.Publish(
		context.WithoutCancel(ctx),
		teamID,
		events.SandboxEvent{
			Version:   events.StructureVersionV2,
			ID:        uuid.New(),
			Type:      eventType.Type,
			Timestamp: time.Now().UTC(),

			EventData:          eventData,
			SandboxID:          sbx.Runtime.SandboxID,
			SandboxExecutionID: sbx.Runtime.ExecutionID,
			SandboxTemplateID:  sbx.Config.BaseTemplateID,
			SandboxBuildID:     buildId,
			SandboxTeamID:      teamID,
		},
	)

	return &emptypb.Empty{}, nil
}

func (s *Server) Pause(ctx context.Context, in *orchestrator.SandboxPauseRequest) (*emptypb.Empty, error) {
	ctx, childSpan := tracer.Start(ctx, "sandbox-pause")
	defer childSpan.End()

	ctx = featureflags.AddToContext(
		ctx,
		ldcontext.NewBuilder(in.GetSandboxId()).
			Kind(featureflags.SandboxKind).
			SetString(featureflags.SandboxTemplateAttribute, in.GetTemplateId()).
			Build(),
	)

	sbx, ok := s.sandboxFactory.Sandboxes.Get(in.GetSandboxId())
	if !ok {
		telemetry.ReportCriticalError(ctx, "sandbox not found", nil, telemetry.WithSandboxID(in.GetSandboxId()))

		return nil, status.Error(codes.NotFound, "sandbox not found")
	}

	marked := s.sandboxFactory.Sandboxes.MarkStopping(ctx, sbx.Runtime.SandboxID, sbx.LifecycleID)
	if !marked {
		telemetry.ReportCriticalError(ctx, "failed to mark sandbox as stopping", nil, telemetry.WithSandboxID(in.GetSandboxId()))

		return nil, status.Error(codes.Internal, "failed to pause sandbox")
	}

	sbxlogger.E(sbx).Info(ctx, "Pausing sandbox")

	// Stop the old sandbox in background after we're done
	defer s.stopSandboxAsync(context.WithoutCancel(ctx), sbx)

	// Fire and forget - upload completes in the background
	res, err := s.snapshotAndCacheSandbox(ctx, sbx, in.GetBuildId())
	if err != nil {
		telemetry.ReportCriticalError(ctx, "error snapshotting sandbox", err, telemetry.WithSandboxID(in.GetSandboxId()))

		return nil, status.Errorf(codes.Internal, "error snapshotting sandbox '%s': %s", in.GetSandboxId(), err)
	}

	s.uploadSnapshotAsync(ctx, sbx, res)

	teamID, buildId, eventData := s.prepareSandboxEventData(ctx, sbx)
	if s.featureFlags.BoolFlag(ctx, featureflags.ExecutionMetricsOnWebhooksFlag) {
		eventData[executionEventDataKey] = s.getSandboxExecutionData(sbx)
	}

	eventType := events.SandboxPausedEventPair
	go s.sbxEventsService.Publish(
		context.WithoutCancel(ctx),
		teamID,
		events.SandboxEvent{
			Version:   events.StructureVersionV2,
			ID:        uuid.New(),
			Type:      eventType.Type,
			Timestamp: time.Now().UTC(),

			EventData:          eventData,
			SandboxID:          sbx.Runtime.SandboxID,
			SandboxExecutionID: sbx.Runtime.ExecutionID,
			SandboxTemplateID:  sbx.Config.BaseTemplateID,
			SandboxBuildID:     buildId,
			SandboxTeamID:      teamID,
		},
	)

	return &emptypb.Empty{}, nil
}

func (s *Server) Checkpoint(ctx context.Context, in *orchestrator.SandboxCheckpointRequest) (*orchestrator.SandboxCheckpointResponse, error) {
	ctx, childSpan := tracer.Start(ctx, "sandbox-checkpoint")
	defer childSpan.End()

	ctx = featureflags.AddToContext(
		ctx,
		ldcontext.NewBuilder(in.GetSandboxId()).
			Kind(featureflags.SandboxKind).
			Build(),
	)

	sbx, ok := s.sandboxFactory.Sandboxes.Get(in.GetSandboxId())
	if !ok {
		telemetry.ReportCriticalError(ctx, "sandbox not found", nil, telemetry.WithSandboxID(in.GetSandboxId()))

		return nil, status.Errorf(codes.NotFound, "sandbox '%s' not found", in.GetSandboxId())
	}

	// Check envd version before snapshotting.
	if err := utils.CheckEnvdVersionForSnapshot(sbx.Config.Envd.Version); err != nil {
		return nil, status.Errorf(codes.FailedPrecondition, "%s", err.Error())
	}

	// skipIfUnchanged short-circuit: ask the in-guest Inspector whether
	// the sandbox has had any recovery-relevant change since the last
	// published checkpoint. This must happen BEFORE MarkStopping so we
	// don't disrupt a running sandbox we're going to leave alone. The
	// path is fully gated: any failure (feature flag off, envd too old,
	// inspector RPC error, no prior checkpoint, degraded inspector)
	// falls through to the existing always-pause path. See issue #2580.
	if in.GetSkipIfUnchanged() && s.featureFlags.BoolFlag(ctx, featureflags.InspectorSkipUnchangedFlag) {
		if last, ok := s.lastPublishedSnapshot.Get(sbx.Runtime.SandboxID); ok {
			if err := utils.CheckEnvdVersionForInspector(sbx.Config.Envd.Version); err == nil {
				inspectCtx, inspectSpan := tracer.Start(ctx, "inspector.query")
				resp, ok := newInspectorClient(s.proxy, sbx.Runtime.SandboxID).QueryChanges(inspectCtx)
				inspectSpan.End()

				if ok && !resp.GetDegraded() && !resp.GetFilesystemChanged() && !resp.GetProcessesChanged() {
					sbxlogger.E(sbx).Info(ctx, "Checkpoint short-circuited: inspector reports no changes",
						zap.String("last_build_id", last.BuildID))
					childSpan.SetAttributes(attribute.Bool("checkpoint.short_circuit", true))
					s.inspectorDecisions.Add(ctx, 1, metric.WithAttributes(attribute.String("decision", "skipped")))
					return &orchestrator.SandboxCheckpointResponse{
						Unchanged:        true,
						PublishedBuildId: last.BuildID,
					}, nil
				}
				// Inspector consulted but not skipped — count as fallthrough
				// (changed or degraded). The full path runs below.
				s.inspectorDecisions.Add(ctx, 1, metric.WithAttributes(attribute.String("decision", "fallthrough")))
			} else {
				s.inspectorDecisions.Add(ctx, 1, metric.WithAttributes(attribute.String("decision", "fallthrough")))
			}
		} else {
			s.inspectorDecisions.Add(ctx, 1, metric.WithAttributes(attribute.String("decision", "fallthrough")))
		}
	}

	// Every Checkpoint that reaches the full path increments the
	// "full" bucket so the ratio of skipped vs full is observable.
	s.inspectorDecisions.Add(ctx, 1, metric.WithAttributes(attribute.String("decision", "full")))

	// Acquire the starting semaphore before resuming, same as Create/Pause.
	if err := s.waitForAcquire(ctx); err != nil {
		return nil, err
	}
	defer s.startingSandboxes.Release(1)

	marked := s.sandboxFactory.Sandboxes.MarkStopping(ctx, sbx.Runtime.SandboxID, sbx.LifecycleID)
	if !marked {
		telemetry.ReportCriticalError(ctx, "failed to mark sandbox as stopping", nil, telemetry.WithSandboxID(in.GetSandboxId()))

		return nil, status.Errorf(codes.Internal, "failed to checkpoint sandbox '%s'", in.GetSandboxId())
	}

	// Always stop the old sandbox when done — on success the resumed sandbox
	// takes over, on failure this prevents a leaked sandbox that is running
	// but no longer addressable through the map. Stop is idempotent.
	defer s.stopSandboxAsync(context.WithoutCancel(ctx), sbx)

	sbxlogger.E(sbx).Info(ctx, "Checkpointing sandbox")

	res, err := s.snapshotAndCacheSandbox(ctx, sbx, in.GetBuildId())
	if err != nil {
		telemetry.ReportCriticalError(ctx, "error snapshotting sandbox for checkpoint", err, telemetry.WithSandboxID(in.GetSandboxId()))

		return nil, status.Errorf(codes.Internal, "error snapshotting sandbox '%s': %s", in.GetSandboxId(), err)
	}

	// Get the template for resume
	template, err := s.templateCache.GetTemplate(ctx, in.GetBuildId(), true, false,
		sbxtemplate.GetTemplateOpts{MaxSandboxLengthHours: sbx.Config.MaxSandboxLengthHours})
	if err != nil {
		telemetry.ReportCriticalError(ctx, "error getting template for resume after checkpoint", err, telemetry.WithSandboxID(in.GetSandboxId()))

		return nil, status.Errorf(codes.Internal, "error getting template for resume: %s", err)
	}

	// Resume the sandbox keeping the same ExecutionID (stable identity for
	// the API, routing catalog, and analytics) but with a fresh LifecycleID
	// so the old sandbox's cleanup goroutine won't
	// accidentally evict the resumed sandbox from the map.
	resumedSbx, err := s.sandboxFactory.ResumeSandbox(
		ctx,
		template,
		sbx.Config,
		sandbox.RuntimeMetadata{
			TemplateID:  sbx.Runtime.TemplateID,
			SandboxID:   sbx.Runtime.SandboxID,
			ExecutionID: sbx.Runtime.ExecutionID,
			TeamID:      sbx.Runtime.TeamID,
			BuildID:     sbx.Runtime.BuildID,
			SandboxType: sbx.Runtime.SandboxType,
		},
		sbx.GetStartedAt(),
		sbx.GetEndAt(),
		sbx.APIStoredConfig,
	)
	if err != nil {
		telemetry.ReportCriticalError(ctx, "error resuming sandbox after checkpoint", err, telemetry.WithSandboxID(in.GetSandboxId()))

		return nil, status.Errorf(codes.Internal, "error resuming sandbox after checkpoint: %s", err)
	}

	// Collect prefetch data immediately after resume while it's most accurate
	prefetchData, prefetchErr := resumedSbx.MemoryPrefetchData(ctx)
	if prefetchErr != nil {
		sbxlogger.I(resumedSbx).Warn(ctx, "failed to get prefetch data for checkpoint", zap.Error(prefetchErr))
	}

	// Setup lifecycle for the resumed sandbox
	s.setupSandboxLifecycle(ctx, resumedSbx)

	// Embed prefetch data into the metadata so it's uploaded with the snapshot files in a single pass.
	if prefetchErr == nil {
		prefetchMapping := metadata.PrefetchEntriesToMapping(slices.Collect(maps.Values(prefetchData.BlockEntries)), prefetchData.BlockSize)
		if prefetchMapping != nil {
			res.meta = res.meta.WithPrefetch(&metadata.Prefetch{
				Memory: prefetchMapping,
			})

			if err := s.templateCache.UpdateMetadata(in.GetBuildId(), res.meta); err != nil {
				sbxlogger.I(resumedSbx).Warn(ctx, "failed to update local metadata with prefetch", zap.Error(err))
			}
		}
	}

	if s.featureFlags.BoolFlag(ctx, featureflags.PeerToPeerAsyncCheckpointFlag) {
		// Async: return immediately; peer nodes can pull chunks from us during the upload window.
		s.uploadSnapshotAsync(ctx, resumedSbx, res)
	} else {
		// Sync: wait for upload before returning so a failed upload is surfaced to the caller.
		// On failure, tear down the resumed sandbox — without a persisted snapshot it cannot
		// be paused or resumed later.
		uploadCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), uploadTimeout)
		defer cancel()

		err := res.upload.Run(uploadCtx)
		defer res.completeUpload(uploadCtx, err)

		if err != nil {
			telemetry.ReportCriticalError(ctx, "error uploading snapshot for checkpoint", err, telemetry.WithSandboxID(in.GetSandboxId()))

			s.sandboxFactory.Sandboxes.MarkStopping(ctx, resumedSbx.Runtime.SandboxID, resumedSbx.LifecycleID)
			s.stopSandboxAsync(context.WithoutCancel(ctx), resumedSbx)

			return nil, status.Errorf(codes.Internal, "error uploading snapshot for checkpoint '%s': %s", in.GetSandboxId(), err)
		}
	}

	s.publishSandboxEvent(ctx, resumedSbx, events.SandboxCheckpointedEvent)

	// Record the published BuildID for the next skip-if-unchanged
	// attempt and reset the inspector epoch so subsequent QueryChanges
	// calls measure changes relative to this checkpoint. Both are
	// best-effort: failures only force the next call to fall through.
	s.lastPublishedSnapshot.Set(resumedSbx.Runtime.SandboxID, in.GetBuildId())

	if utils.CheckEnvdVersionForInspector(resumedSbx.Config.Envd.Version) == nil {
		go newInspectorClient(s.proxy, resumedSbx.Runtime.SandboxID).
			ResetEpoch(context.WithoutCancel(ctx), 0)
	}

	telemetry.ReportEvent(ctx, "Checkpoint completed")

	return &orchestrator.SandboxCheckpointResponse{
		PublishedBuildId: in.GetBuildId(),
	}, nil
}

// Extracts common data needed for sandbox events
func (s *Server) prepareSandboxEventData(ctx context.Context, sbx *sandbox.Sandbox) (uuid.UUID, string, map[string]any) {
	teamID, err := uuid.Parse(sbx.Runtime.TeamID)
	if err != nil {
		sbxlogger.I(sbx).Error(ctx, "error parsing team ID", logger.WithSandboxID(sbx.Runtime.SandboxID), zap.Error(err))
	}

	buildId := ""
	eventData := make(map[string]any)
	if sbx.APIStoredConfig != nil {
		buildId = sbx.APIStoredConfig.GetBuildId()
		if sbx.APIStoredConfig.Metadata != nil {
			// Copy the map to avoid race conditions
			eventData["sandbox_metadata"] = utils.ShallowCopyMap(sbx.APIStoredConfig.GetMetadata())
		}
	}

	return teamID, buildId, eventData
}

func (s *Server) getSandboxExecutionData(sbx *sandbox.Sandbox) map[string]any {
	startedAt := sbx.GetStartedAt()

	return map[string]any{
		"started_at":     startedAt.UTC().Format(time.RFC3339),
		"vcpu_count":     sbx.Config.Vcpu,
		"memory_mb":      sbx.Config.RamMB,
		"execution_time": time.Since(startedAt).Milliseconds(),
	}
}

// snapshotResult holds the data produced by snapshotAndCacheSandbox that
// callers need to start the background remote storage upload.
type snapshotResult struct {
	meta           metadata.Template
	upload         *sandbox.Upload
	completeUpload func(ctx context.Context, uploadErr error)
}

// snapshotAndCacheSandbox creates a snapshot of a sandbox and adds it to the
// local template cache. The caller is responsible for starting the remote
// storage upload via uploadSnapshotAsync.
func (s *Server) snapshotAndCacheSandbox(
	ctx context.Context,
	sbx *sandbox.Sandbox,
	buildID string,
) (*snapshotResult, error) {
	meta, err := sbx.Template.Metadata()
	if err != nil {
		return nil, fmt.Errorf("no metadata found in template: %w", err)
	}

	meta = meta.SameVersionTemplate(metadata.TemplateMetadata{
		BuildID:            buildID,
		KernelVersion:      sbx.Config.FirecrackerConfig.KernelVersion,
		FirecrackerVersion: sbx.Config.FirecrackerConfig.FirecrackerVersion,
	})

	snapshot, err := sbx.Pause(ctx, meta)
	if err != nil {
		return nil, fmt.Errorf("error snapshotting sandbox: %w", err)
	}

	err = s.templateCache.AddSnapshot(
		ctx,
		meta.Template.BuildID,
		snapshot.MemfileDiffHeader,
		snapshot.RootfsDiffHeader,
		snapshot.Snapfile,
		snapshot.Metafile,
		snapshot.MemfileDiff,
		snapshot.RootfsDiff,
	)
	if err != nil {
		return nil, fmt.Errorf("error adding snapshot to template cache: %w", err)
	}

	objectMetadata := storage.ObjectMetadata{
		storage.ObjectMetadataTeamID: sbx.Runtime.TeamID,
	}

	// Register the upload only after the snapshot is in the local cache, so a
	// failed AddSnapshot doesn't leave an orphan future blocking re-registration.
	upload, err := sandbox.NewUpload(ctx, s.uploads, snapshot, s.persistence, s.config.StorageConfig.CompressConfig, s.featureFlags, storage.UseCasePause, objectMetadata)
	if err != nil {
		return nil, fmt.Errorf("register upload: %w", err)
	}

	telemetry.ReportEvent(ctx, "added snapshot to template cache")

	// Capture once so Register and the symmetric Unregister inside
	// completeUpload don't drift if the flag flips mid-upload.
	peerEnabled := s.featureFlags.BoolFlag(ctx, featureflags.PeerToPeerChunkTransferFlag)

	completeUpload := func(ctx context.Context, uploadErr error) {
		upload.Finish(ctx, uploadErr)

		if !peerEnabled {
			return
		}

		s.uploadedBuilds.Set(meta.Template.BuildID, struct{}{}, ttlcache.DefaultTTL)

		if err := s.peerRegistry.Unregister(ctx, meta.Template.BuildID); err != nil {
			logger.L().Warn(ctx, "failed to unregister peer address from routing", zap.String("build_id", meta.Template.BuildID), zap.Error(err))
		}
	}

	if peerEnabled {
		if err := s.peerRegistry.Register(ctx, meta.Template.BuildID, redisPeerKeyTTL); err != nil {
			logger.L().Warn(ctx, "failed to register peer address for routing", zap.String("build_id", meta.Template.BuildID), zap.Error(err))
		}
	}

	return &snapshotResult{
		meta:           meta,
		upload:         upload,
		completeUpload: completeUpload,
	}, nil
}

// uploadSnapshotAsync uploads snapshot files to remote storage in the
// background and cleans up the Redis peer key once done. Used by the Pause
// handler where no prefetch data is available.
func (s *Server) uploadSnapshotAsync(ctx context.Context, sbx *sandbox.Sandbox, res *snapshotResult) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), uploadTimeout)

	go func() {
		defer cancel()

		ctx, span := tracer.Start(ctx, "upload snapshot")
		defer span.End()

		err := res.upload.Run(ctx)
		if err != nil {
			sbxlogger.I(sbx).Error(ctx, "error uploading snapshot files", zap.Error(err))
		} else {
			sbxlogger.I(sbx).Info(ctx, "snapshot finished uploading successfully")
		}

		res.completeUpload(ctx, err)
	}()
}

// setupSandboxLifecycle sets up the cleanup goroutine for a sandbox.
func (s *Server) setupSandboxLifecycle(ctx context.Context, sbx *sandbox.Sandbox) {
	go func() {
		ctx, childSpan := tracer.Start(context.WithoutCancel(ctx), "stop sandbox-lifecycle", trace.WithNewRoot())
		defer childSpan.End()

		waitErr := sbx.Wait(ctx)
		if waitErr != nil {
			sbxlogger.I(sbx).Error(ctx, "failed to wait for sandbox, cleaning up", zap.Error(waitErr))
		}

		cleanupErr := sbx.Close(ctx)
		if cleanupErr != nil {
			sbxlogger.I(sbx).Error(ctx, "failed to cleanup sandbox, will remove from cache", zap.Error(cleanupErr))
		}

		closeErr := s.proxy.RemoveFromPool(sbx.LifecycleID)
		if closeErr != nil {
			sbxlogger.I(sbx).Warn(ctx, "errors when manually closing connections to sandbox", zap.Error(closeErr))
		}

		sbxlogger.E(sbx).Info(ctx, "Sandbox stopped")
	}()
}

// stopSandboxAsync stops the sandbox in a background goroutine.
func (s *Server) stopSandboxAsync(ctx context.Context, sbx *sandbox.Sandbox) {
	go func() {
		ctx, childSpan := tracer.Start(context.WithoutCancel(ctx), "stop sandbox-async", trace.WithNewRoot())
		defer childSpan.End()

		err := sbx.Stop(ctx)
		if err != nil {
			sbxlogger.I(sbx).Error(ctx, "error stopping sandbox", zap.Error(err))
		}
	}()
}

// publishSandboxEvent publishes a sandbox event in the background.
func (s *Server) publishSandboxEvent(ctx context.Context, sbx *sandbox.Sandbox, eventType string) {
	teamID, buildId, eventData := s.prepareSandboxEventData(ctx, sbx)

	go s.sbxEventsService.Publish(
		context.WithoutCancel(ctx),
		teamID,
		events.SandboxEvent{
			Version:   events.StructureVersionV2,
			ID:        uuid.New(),
			Type:      eventType,
			Timestamp: time.Now().UTC(),

			EventData:          eventData,
			SandboxID:          sbx.Runtime.SandboxID,
			SandboxExecutionID: sbx.Runtime.ExecutionID,
			SandboxTemplateID:  sbx.Config.BaseTemplateID,
			SandboxBuildID:     buildId,
			SandboxTeamID:      teamID,
		},
	)
}
