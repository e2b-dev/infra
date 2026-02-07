package server

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/launchdarkly/go-sdk-common/v3/ldcontext"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/fc"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/metadata"
	"github.com/e2b-dev/infra/packages/shared/pkg/events"
	featureflags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
	sandbox_network "github.com/e2b-dev/infra/packages/shared/pkg/sandbox-network"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

var tracer = otel.Tracer("github.com/e2b-dev/infra/packages/orchestrator/internal/server")

const (
	requestTimeout = 60 * time.Second
	// acquireTimeout is the max time to wait for a semaphore for resuming sandboxes snapshot.
	acquireTimeout              = 15 * time.Second
	maxStartingInstancesPerNode = 3
)

func (s *Server) Create(ctx context.Context, req *orchestrator.SandboxCreateRequest) (*orchestrator.SandboxCreateResponse, error) {
	// set max request timeout for this request
	ctx, cancel := context.WithTimeoutCause(ctx, requestTimeout, fmt.Errorf("request timed out"))
	defer cancel()

	// set up tracing
	ctx, childSpan := tracer.Start(ctx, "sandbox-create")
	defer childSpan.End()

	childSpan.SetAttributes(
		telemetry.WithTemplateID(req.GetSandbox().GetTemplateId()),
		attribute.String("kernel.version", req.GetSandbox().GetKernelVersion()),
		telemetry.WithSandboxID(req.GetSandbox().GetSandboxId()),
		attribute.String("client.id", s.info.ClientId),
		attribute.String("envd.version", req.GetSandbox().GetEnvdVersion()),
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
	)

	maxRunningSandboxesPerNode := s.featureFlags.IntFlag(ctx, featureflags.MaxSandboxesPerNode)

	runningSandboxes := s.sandboxes.Count()
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

	sbx, err := s.sandboxFactory.ResumeSandbox(
		ctx,
		template,
		sandbox.Config{
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
				FirecrackerVersion: req.GetSandbox().GetFirecrackerVersion(),
			},
		},
		sandbox.RuntimeMetadata{
			TemplateID:  req.GetSandbox().GetTemplateId(),
			SandboxID:   req.GetSandbox().GetSandboxId(),
			ExecutionID: req.GetSandbox().GetExecutionId(),
			TeamID:      req.GetSandbox().GetTeamId(),
		},
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

func (s *Server) Update(ctx context.Context, req *orchestrator.SandboxUpdateRequest) (*emptypb.Empty, error) {
	ctx, childSpan := tracer.Start(ctx, "sandbox-update")
	defer childSpan.End()

	childSpan.SetAttributes(
		telemetry.WithSandboxID(req.GetSandboxId()),
		attribute.String("client.id", s.info.ClientId),
	)

	sbx, ok := s.sandboxes.Get(req.GetSandboxId())
	if !ok {
		telemetry.ReportCriticalError(ctx, "sandbox not found", nil)

		return nil, status.Error(codes.NotFound, "sandbox not found")
	}

	sbx.EndAt = req.GetEndTime().AsTime()

	teamID, buildId, eventData := s.prepareSandboxEventData(ctx, sbx)
	eventData["set_timeout"] = req.GetEndTime().AsTime().Format(time.RFC3339)
	eventType := events.SandboxUpdatedEventPair

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

func (s *Server) List(ctx context.Context, _ *emptypb.Empty) (*orchestrator.SandboxListResponse, error) {
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

func (s *Server) Delete(ctxConn context.Context, in *orchestrator.SandboxDeleteRequest) (*emptypb.Empty, error) {
	ctx, cancel := context.WithTimeoutCause(ctxConn, requestTimeout, fmt.Errorf("request timed out"))
	defer cancel()

	ctx, childSpan := tracer.Start(ctx, "sandbox-delete")
	defer childSpan.End()

	childSpan.SetAttributes(
		telemetry.WithSandboxID(in.GetSandboxId()),
		attribute.String("client.id", s.info.ClientId),
	)

	sbx, ok := s.sandboxes.Get(in.GetSandboxId())
	if !ok {
		telemetry.ReportCriticalError(ctx, "sandbox not found", nil, telemetry.WithSandboxID(in.GetSandboxId()))

		return nil, status.Errorf(codes.NotFound, "sandbox '%s' not found", in.GetSandboxId())
	}

	sbxlogger.E(sbx).Info(ctx, "Killing sandbox")

	// Remove the sandbox from the cache to prevent loading it again in API during the time the instance is stopping.
	// Old comment:
	// 	Ensure the sandbox is removed from cache.
	// 	Ideally we would rely only on the goroutine defer.
	// Don't allow connecting to the sandbox anymore.
	s.sandboxes.Remove(in.GetSandboxId())

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

	sbx, err := s.acquireSandboxForSnapshot(ctx, in.GetSandboxId())
	if err != nil {
		return nil, err
	}

	sbxlogger.E(sbx).Info(ctx, "Pausing sandbox")

	// Stop the old sandbox in background after we're done
	defer s.stopSandboxAsync(context.WithoutCancel(ctx), sbx)

	// Fire and forget - don't wait for upload to complete
	_, _, err = s.snapshotAndCacheSandbox(ctx, sbx, in.GetBuildId())
	if err != nil {
		telemetry.ReportCriticalError(ctx, "error snapshotting sandbox", err, telemetry.WithSandboxID(in.GetSandboxId()))

		return nil, status.Errorf(codes.Internal, "error snapshotting sandbox '%s': %s", in.GetSandboxId(), err)
	}

	s.publishSandboxEvent(ctx, sbx, events.SandboxPausedEventPair.Type)

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

	sbx, err := s.acquireSandboxForSnapshot(ctx, in.GetSandboxId())
	if err != nil {
		return nil, err
	}

	sbxlogger.E(sbx).Info(ctx, "Checkpointing sandbox")

	// Start snapshot and upload async - we'll wait for upload at the end
	meta, waitForUpload, err := s.snapshotAndCacheSandbox(ctx, sbx, in.GetBuildId())
	if err != nil {
		telemetry.ReportCriticalError(ctx, "error snapshotting sandbox for checkpoint", err, telemetry.WithSandboxID(in.GetSandboxId()))

		return nil, status.Errorf(codes.Internal, "error snapshotting sandbox '%s': %s", in.GetSandboxId(), err)
	}

	// Get the template for resume
	template, err := s.templateCache.GetTemplate(ctx, in.GetBuildId(), true, false)
	if err != nil {
		telemetry.ReportCriticalError(ctx, "error getting template for resume after checkpoint", err, telemetry.WithSandboxID(in.GetSandboxId()))

		return nil, status.Errorf(codes.Internal, "error getting template for resume: %s", err)
	}

	// Resume the sandbox with the same config
	resumedSbx, err := s.sandboxFactory.ResumeSandbox(
		ctx,
		template,
		sbx.Config,
		sandbox.RuntimeMetadata{
			TemplateID:  sbx.Runtime.TemplateID,
			SandboxID:   sbx.Runtime.SandboxID,
			ExecutionID: sbx.Runtime.ExecutionID,
			TeamID:      sbx.Runtime.TeamID,
		},
		sbx.StartedAt,
		sbx.EndAt,
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

	// Stop the old sandbox in background
	s.stopSandboxAsync(context.WithoutCancel(ctx), sbx)

	// Upload prefetch mapping in background
	if prefetchErr == nil {
		s.uploadPrefetchMappingAsync(ctx, resumedSbx, meta, prefetchData)
	}

	s.publishSandboxEvent(ctx, resumedSbx, "sandbox.checkpointed")

	// Wait for snapshot upload to complete before returning
	if err := waitForUpload(); err != nil {
		telemetry.ReportCriticalError(ctx, "error uploading snapshot for checkpoint", err, telemetry.WithSandboxID(in.GetSandboxId()))

		return nil, status.Errorf(codes.Internal, "error uploading snapshot for checkpoint '%s': %s", in.GetSandboxId(), err)
	}

	telemetry.ReportEvent(ctx, "Checkpoint completed")

	return &orchestrator.SandboxCheckpointResponse{}, nil
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

// snapshotAndCacheSandbox creates a snapshot of a sandbox, adds it to cache, and starts uploading async.
// Returns the metadata and a wait function. Call the wait function to block until upload completes.
// If you don't need to wait for the upload, simply don't call the wait function (fire and forget).
func (s *Server) snapshotAndCacheSandbox(
	ctx context.Context,
	sbx *sandbox.Sandbox,
	buildID string,
) (metadata.Template, func() error, error) {
	meta, err := sbx.Template.Metadata()
	if err != nil {
		return metadata.Template{}, nil, fmt.Errorf("no metadata found in template: %w", err)
	}

	meta = meta.SameVersionTemplate(metadata.TemplateMetadata{
		BuildID:            buildID,
		KernelVersion:      sbx.Config.FirecrackerConfig.KernelVersion,
		FirecrackerVersion: sbx.Config.FirecrackerConfig.FirecrackerVersion,
	})

	snapshot, err := sbx.Pause(ctx, meta)
	if err != nil {
		return metadata.Template{}, nil, fmt.Errorf("error snapshotting sandbox: %w", err)
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
		return metadata.Template{}, nil, fmt.Errorf("error adding snapshot to template cache: %w", err)
	}

	telemetry.ReportEvent(ctx, "added snapshot to template cache")

	// Start upload in background, return a wait function
	uploadCtx := context.WithoutCancel(ctx)
	errCh := make(chan error, 1)

	go func() {
		err := snapshot.Upload(uploadCtx, s.persistence, storage.TemplateFiles{BuildID: meta.Template.BuildID})
		if err != nil {
			sbxlogger.I(sbx).Error(uploadCtx, "error uploading snapshot", zap.Error(err))
			errCh <- err

			return
		}

		logger.L().Info(uploadCtx, "Snapshot uploaded successfully", logger.WithSandboxID(sbx.Runtime.SandboxID))
		errCh <- nil
	}()

	waitForUpload := func() error {
		return <-errCh
	}

	return meta, waitForUpload, nil
}

// setupSandboxLifecycle adds the sandbox to the map and sets up the cleanup goroutine.
func (s *Server) setupSandboxLifecycle(ctx context.Context, sbx *sandbox.Sandbox) {
	ctx, span := tracer.Start(ctx, "setup sandbox-lifecycle")
	defer span.End()

	s.sandboxes.Insert(sbx)

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

		s.sandboxes.RemoveByExecutionID(sbx.Runtime.SandboxID, sbx.Runtime.ExecutionID)

		closeErr := s.proxy.RemoveFromPool(sbx.Runtime.ExecutionID)
		if closeErr != nil {
			sbxlogger.I(sbx).Warn(ctx, "errors when manually closing connections to sandbox", zap.Error(closeErr))
		}

		sbxlogger.E(sbx).Info(ctx, "Sandbox stopped")
	}()
}

// acquireSandboxForSnapshot locks the pause mutex, retrieves the sandbox, removes it from the map,
// and unlocks. Returns the sandbox for snapshotting or an error if not found.
func (s *Server) acquireSandboxForSnapshot(ctx context.Context, sandboxID string) (*sandbox.Sandbox, error) {
	s.pauseMu.Lock()

	sbx, ok := s.sandboxes.Get(sandboxID)
	if !ok {
		s.pauseMu.Unlock()

		telemetry.ReportCriticalError(ctx, "sandbox not found", nil)

		return nil, status.Error(codes.NotFound, "sandbox not found")
	}

	s.sandboxes.Remove(sandboxID)

	s.pauseMu.Unlock()

	return sbx, nil
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

// uploadPrefetchMappingAsync uploads prefetch mapping to metadata in background.
func (s *Server) uploadPrefetchMappingAsync(ctx context.Context, sbx *sandbox.Sandbox, meta metadata.Template, prefetchData block.PrefetchData) {
	go func(ctx context.Context) {
		ctx, childSpan := tracer.Start(ctx, "upload-prefetch-mapping", trace.WithNewRoot())
		defer childSpan.End()

		prefetchMapping := metadata.PrefetchEntriesToMapping(slices.Collect(maps.Values(prefetchData.BlockEntries)), prefetchData.BlockSize)
		if prefetchMapping == nil {
			sbxlogger.I(sbx).Debug(ctx, "no prefetch mapping collected")

			return
		}

		updatedMeta := meta.WithPrefetch(&metadata.Prefetch{
			Memory: prefetchMapping,
		})

		err := metadata.UploadMetadata(ctx, s.persistence, updatedMeta)
		if err != nil {
			sbxlogger.I(sbx).Warn(ctx, "failed to upload prefetch metadata", zap.Error(err))

			return
		}

		s.templateCache.Invalidate(meta.Template.BuildID)

		sbxlogger.I(sbx).Info(ctx, "prefetch mapping uploaded",
			zap.Int("block_count", prefetchMapping.Count()))
	}(context.WithoutCancel(ctx))
}
