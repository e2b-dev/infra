//go:build linux

package server

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"syscall"
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
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/build"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/fc"
	sbxtemplate "github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/template"
	buildenvd "github.com/e2b-dev/infra/packages/orchestrator/pkg/template/build/core/envd"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/template/metadata"
	"github.com/e2b-dev/infra/packages/shared/pkg/events"
	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/retry"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

var tracer = otel.Tracer("github.com/e2b-dev/infra/packages/orchestrator/pkg/server")

const (
	requestTimeout = 60 * time.Second
	// acquireTimeout is the max time to wait for a semaphore for resuming sandboxes snapshot.
	acquireTimeout = 15 * time.Second

	// uploadTimeout is the max time allowed for a single upload attempt to
	// remote storage. The overall retry window is uploadTotalBudget.
	uploadTimeout = 20 * time.Minute
	// uploadTotalBudget bounds how long a snapshot upload is retried before it
	// is given up. Covers a long GCS outage without retrying forever.
	uploadTotalBudget = 2 * time.Hour
	// redisPeerKeyTTL keeps the peer routing key valid across the whole retry
	// window so a long retry doesn't drop peer routing mid-upload. It is
	// unregistered promptly once the upload finishes (success or give-up).
	redisPeerKeyTTL = uploadTotalBudget + 2*time.Minute

	// uploadRetryInitialBackoff is the wait before the first retry; it grows
	// exponentially up to uploadRetryMaxBackoff.
	uploadRetryInitialBackoff = 5 * time.Second
	// uploadRetryMaxBackoff caps the backoff between attempts.
	uploadRetryMaxBackoff = 2 * time.Minute
	// uploadRetryBackoffMultiplier is the exponential growth factor between
	// retry attempts.
	uploadRetryBackoffMultiplier = 2

	// executionEventDataKey is the key used in webhook event data for sandbox execution metrics.
	executionEventDataKey = "execution"

	killReasonUnknown = "unknown"
)

func (s *Server) Create(ctx context.Context, req *orchestrator.SandboxCreateRequest) (_ *orchestrator.SandboxCreateResponse, createErr error) {
	// set max request timeout for this request
	ctx, cancel := context.WithTimeoutCause(ctx, requestTimeout, errors.New("request timed out"))
	defer cancel()

	// set up tracing
	ctx, childSpan := tracer.Start(ctx, "sandbox-create")
	defer childSpan.End()

	isResume := req.GetSandbox().GetSnapshot()
	// fsOnly is set at the resume fork below when this takes the filesystem-only
	// reboot path (vs a memory restore), so create/resume e2e latency can be
	// split reboot vs memory — mirroring the fs_only pause label. Combined with
	// sandbox.resume: resume=false → fresh create; resume=true,fs_only=false →
	// memory resume; resume=true,fs_only=true → filesystem-only reboot.
	var fsOnly bool
	createStart := time.Now()
	// Set by maybeUpgradeEnvd below; labels the resume-latency histogram so the
	// treated (upgraded) vs untreated cohorts can be compared during the rollout.
	var envdUpgraded bool
	defer func() {
		s.sandboxCreateDuration.Record(ctx, time.Since(createStart).Milliseconds(),
			metric.WithAttributes(
				attribute.Bool("sandbox.resume", isResume),
				attribute.Bool("fs_only", fsOnly),
				attribute.Bool("success", createErr == nil),
				attribute.Bool("envd.upgraded", envdUpgraded),
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
			SetString(featureflags.SandboxEnvdVersionAttribute, req.GetSandbox().GetEnvdVersion()).
			Build(),
		ldcontext.NewBuilder(req.GetSandbox().GetTeamId()).
			Kind(featureflags.TeamKind).
			Build(),
	)

	// BYOP egress proxy kill-switch; mirrors the API gate for direct gRPC
	// callers and snapshot resumes.
	if req.GetSandbox().GetNetwork().GetEgress().GetEgressProxyAddress() != "" {
		if !s.featureFlags.BoolFlag(ctx, featureflags.BYOPProxyEnabledFlag) {
			telemetry.ReportEvent(ctx, "egressProxy rejected by BYOPProxyEnabledFlag")

			return nil, status.Error(codes.PermissionDenied,
				"egress proxy is not enabled for this team")
		}
		if !s.sandboxFactory.EgressProxy().SupportsBYOP() {
			telemetry.ReportEvent(ctx, "egressProxy rejected: orchestrator build has no BYOP dialer")

			return nil, status.Error(codes.Unimplemented,
				"egress proxy is not supported by this orchestrator build")
		}
	}

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

	// A filesystem-only snapshot has no memory to restore; resume it by
	// cold-booting (rebooting) from its rootfs. The snapshot's own metadata is
	// the source of truth, so a memory snapshot can never be rebooted.
	meta, err := template.Metadata()
	if err != nil {
		return nil, fmt.Errorf("failed to read template metadata: %w", err)
	}

	var sbx *sandbox.Sandbox
	if meta.IsFilesystemOnly() {
		fsOnly = true
		sbx, err = s.sandboxFactory.RebootSandbox(
			ctx,
			template,
			config,
			runtime,
			req.GetEndTime().AsTime(),
			req.GetSandbox(),
			// Defer routing until after the resume-time envd upgrade's
			// post-/init, so the sandbox isn't reachable during its pre-init
			// auth window. Promoted below via markSandboxLive.
			true,
		)
	} else {
		sbx, err = s.sandboxFactory.ResumeSandbox(
			ctx,
			template,
			config,
			runtime,
			req.GetStartTime().AsTime(),
			req.GetEndTime().AsTime(),
			req.GetSandbox(),
			// Defer routing until after the resume-time envd upgrade's
			// post-/init (see markSandboxLive below).
			sandbox.WithDeferredLiveRegistration(),
		)
	}
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

	// Resume-time envd live-upgrade. The API /resume maps to Create
	// with snapshot=true, so this is the real resume path. Flag-driven,
	// best-effort + recover-wrapped (see maybeUpgradeEnvd) so it can't disrupt
	// resume. ctx already carries the LD context (envd-version/team/template).
	if req.GetSandbox().GetSnapshot() {
		var upErr error
		envdUpgraded, upErr = s.maybeUpgradeEnvd(ctx, sbx)
		if upErr != nil {
			// Only an unrecoverable post-execve failure (new envd left
			// uninitialized) returns an error; fail the resume rather than hand
			// back a bricked sandbox. MarkRunning is deferred until markSandboxLive
			// below, so the sandbox is not yet in the live registry — MarkStopping
			// is a no-op here and stopSandboxAsync does the physical teardown.
			sbx.SetStopReason(sandbox.StopReasonKilled)
			s.sandboxFactory.Sandboxes.MarkStopping(ctx, sbx.Runtime.SandboxID, sbx.LifecycleID)
			s.stopSandboxAsync(context.WithoutCancel(ctx), sbx)

			return nil, upErr
		}
	}

	// Promote to the live registry only now — after any resume-time envd upgrade
	// has run its post-/init and restored the access token — so the sandbox is
	// never routable during the upgrade's sub-second pre-init auth window. Both
	// the resume and reboot paths above defer this.
	s.markSandboxLive(ctx, sbx)

	// Read scheduling metadata after the sandbox resumed so the template's
	// memfile/rootfs devices (and their headers) are resolved.
	var schedulingMetadata *orchestrator.SchedulingMetadata
	if provider, ok := template.(interface {
		SchedulingMetadata(ctx context.Context) *orchestrator.SchedulingMetadata
	}); ok {
		schedulingMetadata = provider.SchedulingMetadata(ctx)
	}

	eventType := events.SandboxCreatedEventPair
	if req.GetSandbox().GetSnapshot() {
		eventType = events.SandboxResumedEventPair
	}

	teamID, buildId, eventsTTLDays, eventData := s.prepareSandboxEventData(ctx, sbx)
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
			EventsTTLDays:      eventsTTLDays,
		},
	)

	return &orchestrator.SandboxCreateResponse{
		ClientId:           s.info.ClientId,
		SchedulingMetadata: schedulingMetadata,
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
	)

	sbx, ok := s.sandboxFactory.Sandboxes.Get(req.GetSandboxId())
	if !ok {
		telemetry.ReportCriticalError(ctx, "sandbox not found", nil)

		return nil, status.Error(codes.NotFound, "sandbox not found")
	}

	childSpan.SetAttributes(
		telemetry.WithTeamID(sbx.Runtime.TeamID),
		telemetry.WithTemplateID(sbx.Runtime.TemplateID),
		telemetry.WithBuildID(sbx.Runtime.BuildID),
		telemetry.WithFirecrackerVersion(sbx.Config.FirecrackerConfig.FirecrackerVersion),
		telemetry.WithKernelVersion(sbx.Config.FirecrackerConfig.KernelVersion),
		telemetry.WithEnvdVersion(sbx.Config.Envd.Version),
	)

	// Mirror the Create-side BYOP gates; defense-in-depth for direct gRPC
	// callers.
	if req.GetEgress().GetEgressProxyAddress() != "" {
		ctx = featureflags.AddToContext(ctx,
			ldcontext.NewBuilder(sbx.Runtime.TeamID).
				Kind(featureflags.TeamKind).
				Build(),
		)
		if !s.featureFlags.BoolFlag(ctx, featureflags.BYOPProxyEnabledFlag) {
			telemetry.ReportEvent(ctx, "egressProxy update rejected by BYOPProxyEnabledFlag")

			return nil, status.Error(codes.PermissionDenied,
				"egress proxy is not enabled for this team")
		}
		if !s.sandboxFactory.EgressProxy().SupportsBYOP() {
			telemetry.ReportEvent(ctx, "egressProxy update rejected: orchestrator build has no BYOP dialer")

			return nil, status.Error(codes.Unimplemented,
				"egress proxy is not supported by this orchestrator build")
		}
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
			egress := req.GetEgress()

			if err := transitionEgress(ctx, sbx.Config, sbx.Slot.UpdateInternet, oldEgress, egress); err != nil {
				return nil, err
			}

			return func(ctx context.Context) {
				// Fails closed: a failed kernel re-drop never publishes the
				// weaker config. Rollbacks cannot propagate errors, so report.
				if err := transitionEgress(ctx, sbx.Config, sbx.Slot.UpdateInternet, sbx.Config.GetNetworkEgress(), oldEgress); err != nil {
					telemetry.ReportCriticalError(ctx, "failed to roll back sandbox egress update", err)
				}
			}, nil
		})
	}

	err := sbx.RunUpdate(func() error {
		if err := utils.ApplyAllOrNone(ctx, updates); err != nil {
			telemetry.ReportCriticalError(ctx, "failed to update sandbox", err)

			return status.Errorf(codes.Internal, "failed to update sandbox: %s", err)
		}

		// Publish event if any updates were applied.
		if len(updates) > 0 {
			teamID, buildId, eventsTTLDays, eventData := s.prepareSandboxEventData(ctx, sbx)
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
					EventsTTLDays:      eventsTTLDays,
				},
			)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return &emptypb.Empty{}, nil
}

// transitionEgress moves the in-memory egress config (read per-connection by
// the userspace proxy) and the in-netns kernel firewall from one state to
// another. Loosen last, tighten first: the kernel must never be more relaxed
// than the config the proxy sees. On failure both layers stay consistent.
// updateInternet is a parameter so tests can observe the ordering.
func transitionEgress(
	ctx context.Context,
	cfg *sandbox.Config,
	updateInternet func(context.Context, *orchestrator.SandboxNetworkEgressConfig) error,
	from, to *orchestrator.SandboxNetworkEgressConfig,
) error {
	if to.GetEgressProxyAddress() != "" {
		// BYOP loosens the kernel firewall: internal-destined TCP is handed
		// to the userspace proxy instead of dropped, so the proxy must see
		// the config before the kernel lets that traffic through.
		applyNetworkEgress(cfg, to)
		if err := updateInternet(ctx, to); err != nil {
			// The kernel kept its rules (atomic flush); undo the publish.
			applyNetworkEgress(cfg, from)

			return fmt.Errorf("failed to update sandbox network: %w", err)
		}

		return nil
	}

	if err := updateInternet(ctx, to); err != nil {
		return fmt.Errorf("failed to update sandbox network: %w", err)
	}
	applyNetworkEgress(cfg, to)

	return nil
}

// applyNetworkEgress publishes the egress config to the in-memory sandbox
// config, collapsing an all-empty egress (no CIDRs, domains, rules, or proxy)
// to nil so readers treat it as "no custom egress".
func applyNetworkEgress(cfg *sandbox.Config, egress *orchestrator.SandboxNetworkEgressConfig) {
	if len(egress.GetAllowedCidrs()) == 0 && len(egress.GetDeniedCidrs()) == 0 && len(egress.GetAllowedDomains()) == 0 && len(egress.GetRules()) == 0 && egress.GetEgressProxyAddress() == "" {
		cfg.SetNetworkEgress(nil)
	} else {
		cfg.SetNetworkEgress(egress)
	}
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
	)

	sbx, ok := s.sandboxFactory.Sandboxes.Get(in.GetSandboxId())
	if !ok {
		telemetry.ReportCriticalError(ctx, "sandbox not found", nil, telemetry.WithSandboxID(in.GetSandboxId()))

		return nil, status.Errorf(codes.NotFound, "sandbox '%s' not found", in.GetSandboxId())
	}

	childSpan.SetAttributes(
		telemetry.WithTeamID(sbx.Runtime.TeamID),
		telemetry.WithTemplateID(sbx.Runtime.TemplateID),
		telemetry.WithBuildID(sbx.Runtime.BuildID),
		telemetry.WithFirecrackerVersion(sbx.Config.FirecrackerConfig.FirecrackerVersion),
		telemetry.WithKernelVersion(sbx.Config.FirecrackerConfig.KernelVersion),
		telemetry.WithEnvdVersion(sbx.Config.Envd.Version),
	)

	// Mark the sandbox as stopping so it is excluded from live queries (Get, Items,
	// Count) but remains findable by IP (GetByHostPort) while the Firecracker
	// process finishes shutting down.
	// This prevents the sandbox from being synced to API again.
	marked := s.sandboxFactory.Sandboxes.MarkStopping(ctx, sbx.Runtime.SandboxID, sbx.LifecycleID)
	if !marked {
		telemetry.ReportCriticalError(ctx, "failed to mark sandbox as stopping", nil, telemetry.WithSandboxID(in.GetSandboxId()))

		return nil, status.Errorf(codes.Internal, "failed to delete sandbox '%s'", in.GetSandboxId())
	}

	killReason := in.GetKillReason()
	if killReason == "" {
		killReason = killReasonUnknown
	}

	sbxlogger.E(sbx).Info(ctx, "Killing sandbox", zap.String("kill_reason", killReason))

	sbx.SetStopReason(sandbox.StopReasonKilled)

	// Check health metrics before stopping the sandbox
	sbx.Checks.Healthcheck(ctx, true)

	// Start the cleanup in a goroutine—the initial kill request should be send as the first thing in stop, and at this point you cannot route to the sandbox anymore.
	// We don't wait for the whole cleanup to finish here.
	go func() {
		err := sbx.Stop(context.WithoutCancel(ctx))
		if err != nil {
			sbxlogger.I(sbx).Error(ctx, "error stopping sandbox",
				logger.WithSandboxID(in.GetSandboxId()),
				zap.String("kill_reason", killReason),
				zap.Error(err),
			)
		}
	}()

	teamID, buildId, eventsTTLDays, eventData := s.prepareSandboxEventData(ctx, sbx)
	eventData[executionEventDataKey] = s.getSandboxExecutionData(sbx)
	addKillReason(eventData, killReason)
	recordSandboxKill(ctx, s.sandboxKilledCounter, killReason)

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
			EventsTTLDays:      eventsTTLDays,
		},
	)

	return &emptypb.Empty{}, nil
}

// addKillReason records the kill reason on killed events. Empty input is
// normalized to "unknown" so killed events always carry a kill_reason key.
func addKillReason(eventData map[string]any, killReason string) {
	if killReason == "" {
		killReason = killReasonUnknown
	}

	eventData["kill_reason"] = killReason
}

// recordSandboxKill increments the kill counter with a bounded reason label.
func recordSandboxKill(ctx context.Context, counter metric.Int64Counter, killReason string) {
	if killReason == "" {
		killReason = killReasonUnknown
	}

	counter.Add(ctx, 1, metric.WithAttributes(attribute.String("kill_reason", killReason)))
}

// recordExecutionDuration samples how long one sandbox execution ran, labeled
// by why it ended. An operation that fails after the guest has already stopped
// — a pause whose snapshot fails — is still labeled by its intent; its failure
// is counted by that operation's own metric.
func (s *Server) recordExecutionDuration(ctx context.Context, sbx *sandbox.Sandbox) {
	duration, ok := sbx.ExecutionDuration()
	if !ok {
		return
	}

	s.sandboxExecutionDuration.Record(ctx, duration.Milliseconds(),
		metric.WithAttributes(attribute.String("stop_reason", string(sbx.GetStopReason()))))
}

func (s *Server) Pause(ctx context.Context, in *orchestrator.SandboxPauseRequest) (resp *orchestrator.SandboxPauseResponse, err error) {
	ctx, childSpan := tracer.Start(ctx, "sandbox-pause")
	defer childSpan.End()

	// Record pause duration split by fs_only vs memory (the gRPC RPC metric
	// can't distinguish them) and success, so dashboards can scope pause
	// call-count / error-rate / latency to filesystem-only pauses.
	pauseStart := time.Now()
	defer func() {
		s.sandboxPauseDuration.Record(ctx, time.Since(pauseStart).Milliseconds(),
			metric.WithAttributes(
				attribute.Bool("fs_only", in.GetFilesystemOnly()),
				attribute.Bool("success", err == nil),
			),
		)
	}()

	childSpan.SetAttributes(
		telemetry.WithSandboxID(in.GetSandboxId()),
		telemetry.WithTemplateID(in.GetTemplateId()),
		telemetry.WithBuildID(in.GetBuildId()),
	)

	sbx, ok := s.sandboxFactory.Sandboxes.Get(in.GetSandboxId())
	if !ok {
		telemetry.ReportCriticalError(ctx, "sandbox not found", nil, telemetry.WithSandboxID(in.GetSandboxId()))

		return nil, status.Error(codes.NotFound, "sandbox not found")
	}

	ctx = featureflags.AddToContext(
		ctx,
		ldcontext.NewBuilder(in.GetSandboxId()).
			Kind(featureflags.SandboxKind).
			SetString(featureflags.SandboxTemplateAttribute, sbx.Runtime.TemplateID).
			SetString(featureflags.SandboxKernelVersionAttribute, sbx.Config.FirecrackerConfig.KernelVersion).
			SetString(featureflags.SandboxFirecrackerVersionAttribute, sbx.Config.FirecrackerConfig.FirecrackerVersion).
			SetString(featureflags.SandboxEnvdVersionAttribute, sbx.Config.Envd.Version).
			Build(),
	)

	childSpan.SetAttributes(
		telemetry.WithTeamID(sbx.Runtime.TeamID),
		telemetry.WithFirecrackerVersion(sbx.Config.FirecrackerConfig.FirecrackerVersion),
		telemetry.WithKernelVersion(sbx.Config.FirecrackerConfig.KernelVersion),
		telemetry.WithEnvdVersion(sbx.Config.Envd.Version),
	)

	marked := s.sandboxFactory.Sandboxes.MarkStopping(ctx, sbx.Runtime.SandboxID, sbx.LifecycleID)
	if !marked {
		telemetry.ReportCriticalError(ctx, "failed to mark sandbox as stopping", nil, telemetry.WithSandboxID(in.GetSandboxId()))

		return nil, status.Error(codes.Internal, "failed to pause sandbox")
	}

	sbxlogger.E(sbx).Info(ctx, "Pausing sandbox")

	// Set before the snapshot, not after it succeeds: snapshotting suspends the
	// guest and can close the sandbox, which would read as a crash.
	sbx.SetStopReason(sandbox.StopReasonPaused)

	// Stop the old sandbox in background after we're done
	defer s.stopSandboxAsync(context.WithoutCancel(ctx), sbx)

	// Defer the rootfs reflink off the pause critical path when enabled: pause is a
	// suspend, so nothing reads the diff until a later resume (which waits on the
	// upload anyway). NBD provider only; falls back to synchronous export otherwise.
	deferRootfsExport := s.featureFlags.BoolFlag(ctx, featureflags.DeferRootfsExportFlag)

	// Fire and forget - upload completes in the background
	res, err := s.snapshotAndCacheSandbox(ctx, sbx, in.GetBuildId(), map[string]string{storage.ObjectMetadataTemplateID: in.GetTemplateId()}, storage.ObjectOriginPause, in.GetFilesystemOnly(), deferRootfsExport)
	if err != nil {
		telemetry.ReportCriticalError(ctx, "error snapshotting sandbox", err, telemetry.WithSandboxID(in.GetSandboxId()))

		return nil, status.Errorf(codes.Internal, "error snapshotting sandbox '%s': %s", in.GetSandboxId(), err)
	}

	s.uploadSnapshotAsync(ctx, sbx, res)

	// Best-effort: the local snapshot is now in the cache and the remote upload
	// has been kicked off above (still in flight). Harvest a resume page-fault
	// trace from a throwaway warm resume of the local snapshot and (when enabled)
	// persist it as a prefetch mapping for the next resume. Runs in the
	// background; never affects the pause result, and waits for the upload before
	// touching metadata. No-op unless the harvest flag is on. Reuse the object
	// metadata the snapshot was uploaded with so the re-upload can't drift.
	//
	// Skip it for a filesystem-only pause: that snapshot has no memory diff, so a
	// memory resume of it would just fail (the resume is reserved for memory
	// snapshots; fs-only is a reboot) — there is no memory working set to harvest.
	if !in.GetFilesystemOnly() {
		s.harvestResumePrefetchAsync(ctx, sbx, res, in.GetBuildId(), res.objectMetadata)
	}

	teamID, buildId, eventsTTLDays, eventData := s.prepareSandboxEventData(ctx, sbx)
	eventData[executionEventDataKey] = s.getSandboxExecutionData(sbx)

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
			EventsTTLDays:      eventsTTLDays,
		},
	)

	return &orchestrator.SandboxPauseResponse{
		SchedulingMetadata: res.schedulingMetadata,
	}, nil
}

func (s *Server) Checkpoint(ctx context.Context, in *orchestrator.SandboxCheckpointRequest) (*orchestrator.SandboxCheckpointResponse, error) {
	ctx, childSpan := tracer.Start(ctx, "sandbox-checkpoint")
	defer childSpan.End()

	childSpan.SetAttributes(
		telemetry.WithSandboxID(in.GetSandboxId()),
		telemetry.WithBuildID(in.GetBuildId()),
	)

	sbx, ok := s.sandboxFactory.Sandboxes.Get(in.GetSandboxId())
	if !ok {
		telemetry.ReportCriticalError(ctx, "sandbox not found", nil, telemetry.WithSandboxID(in.GetSandboxId()))

		return nil, status.Errorf(codes.NotFound, "sandbox '%s' not found", in.GetSandboxId())
	}

	ctx = featureflags.AddToContext(
		ctx,
		ldcontext.NewBuilder(in.GetSandboxId()).
			Kind(featureflags.SandboxKind).
			SetString(featureflags.SandboxTemplateAttribute, sbx.Runtime.TemplateID).
			SetString(featureflags.SandboxKernelVersionAttribute, sbx.Config.FirecrackerConfig.KernelVersion).
			SetString(featureflags.SandboxFirecrackerVersionAttribute, sbx.Config.FirecrackerConfig.FirecrackerVersion).
			SetString(featureflags.SandboxEnvdVersionAttribute, sbx.Config.Envd.Version).
			Build(),
	)

	childSpan.SetAttributes(
		telemetry.WithTeamID(sbx.Runtime.TeamID),
		telemetry.WithTemplateID(sbx.Runtime.TemplateID),
		telemetry.WithFirecrackerVersion(sbx.Config.FirecrackerConfig.FirecrackerVersion),
		telemetry.WithKernelVersion(sbx.Config.FirecrackerConfig.KernelVersion),
		telemetry.WithEnvdVersion(sbx.Config.Envd.Version),
	)

	// Check envd version before snapshotting.
	if err := utils.CheckEnvdVersionForSnapshot(sbx.Config.Envd.Version); err != nil {
		return nil, status.Errorf(codes.FailedPrecondition, "%s", err.Error())
	}

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

	// Set before the snapshot, as in Pause.
	sbx.SetStopReason(sandbox.StopReasonCheckpointing)

	// Checkpoint always takes a full memory snapshot; filesystem-only checkpoint
	// (resume-in-place would need to reboot) is not supported yet.
	// Checkpoint resumes a fresh sandbox from the new build immediately, so the
	// diff must be materialized synchronously — never defer the rootfs export here.
	res, err := s.snapshotAndCacheSandbox(ctx, sbx, in.GetBuildId(), in.GetMetadata(), storage.ObjectOriginSnapshotTemplate, false, false)
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
		// Defer routing until after the upgrade's post-/init (markSandboxLive below).
		sandbox.WithDeferredLiveRegistration(),
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

	// resume-time envd live-upgrade. Best-effort and tightly gated so
	// it can never disrupt the universal resume path — except an unrecoverable
	// post-execve failure (new envd left uninitialized), which fails the
	// checkpoint rather than leave a bricked sandbox.
	if _, upErr := s.maybeUpgradeEnvd(ctx, resumedSbx); upErr != nil {
		// Bricked past the execve — tear the resumed sandbox down. MarkRunning is
		// deferred until markSandboxLive below, so the sandbox is not yet in the
		// live registry: MarkStopping is a no-op and stopSandboxAsync does the
		// physical teardown.
		resumedSbx.SetStopReason(sandbox.StopReasonKilled)
		s.sandboxFactory.Sandboxes.MarkStopping(ctx, resumedSbx.Runtime.SandboxID, resumedSbx.LifecycleID)
		s.stopSandboxAsync(context.WithoutCancel(ctx), resumedSbx)

		return nil, upErr
	}

	// Promote to the live registry now that any resume-time upgrade's post-/init
	// has restored auth — the sandbox was resumed with routing deferred.
	s.markSandboxLive(ctx, resumedSbx)

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

			resumedSbx.SetStopReason(sandbox.StopReasonKilled)
			s.sandboxFactory.Sandboxes.MarkStopping(ctx, resumedSbx.Runtime.SandboxID, resumedSbx.LifecycleID)
			s.stopSandboxAsync(context.WithoutCancel(ctx), resumedSbx)

			return nil, status.Errorf(codes.Internal, "error uploading snapshot for checkpoint '%s': %s", in.GetSandboxId(), err)
		}
	}

	s.publishSandboxEvent(ctx, resumedSbx, events.SandboxCheckpointedEvent)

	telemetry.ReportEvent(ctx, "Checkpoint completed")

	return &orchestrator.SandboxCheckpointResponse{
		SchedulingMetadata: res.schedulingMetadata,
	}, nil
}

// Extracts common data needed for sandbox events
func (s *Server) prepareSandboxEventData(ctx context.Context, sbx *sandbox.Sandbox) (uuid.UUID, string, int64, map[string]any) {
	teamID, err := uuid.Parse(sbx.Runtime.TeamID)
	if err != nil {
		sbxlogger.I(sbx).Error(ctx, "error parsing team ID", logger.WithSandboxID(sbx.Runtime.SandboxID), zap.Error(err))
	}

	buildId := ""
	eventsTTLDays := int64(0)
	eventData := make(map[string]any)
	if sbx.APIStoredConfig != nil {
		buildId = sbx.APIStoredConfig.GetBuildId()
		eventsTTLDays = sbx.APIStoredConfig.GetEventsTtlDays()
		if sbx.APIStoredConfig.Metadata != nil {
			// Copy the map to avoid race conditions
			eventData["sandbox_metadata"] = utils.ShallowCopyMap(sbx.APIStoredConfig.GetMetadata())
		}
	}

	return teamID, buildId, eventsTTLDays, eventData
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
	meta               metadata.Template
	schedulingMetadata *orchestrator.SchedulingMetadata
	upload             *sandbox.Upload
	completeUpload     func(ctx context.Context, uploadErr error)
	// rootfsDiff is the snapshot's rootfs diff. With deferred export it is a
	// promise-backed diff that resolves only once the background seal finishes,
	// so the prefetch harvest waits on its CachePath before its throwaway resume
	// (a warm resume reads the rootfs). Nil-safe callers only: always set here.
	rootfsDiff build.Diff
	// objectMetadata is the storage object metadata the snapshot was uploaded
	// with. The prefetch harvest reuses it verbatim when re-uploading the
	// metadata object, so the two can never drift.
	objectMetadata storage.ObjectMetadata
	// filesystemOnly records whether this was a filesystem-only (memoryless)
	// pause, so the async upload can label its failure counter with fs_only.
	filesystemOnly bool
}

// snapshotAndCacheSandbox creates a snapshot of a sandbox and adds it to the
// local template cache. The caller is responsible for starting the remote
// storage upload via uploadSnapshotAsync.
func (s *Server) snapshotAndCacheSandbox(
	ctx context.Context,
	sbx *sandbox.Sandbox,
	buildID string,
	provenance map[string]string,
	buildOrigin storage.ObjectOrigin,
	filesystemOnly bool,
	deferRootfsExport bool,
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

	var pauseOpts []sandbox.PauseOption
	if filesystemOnly {
		pauseOpts = append(pauseOpts, sandbox.WithFilesystemSnapshot())
	}
	if deferRootfsExport {
		pauseOpts = append(pauseOpts, sandbox.WithDeferredRootfsExport())
	}

	snapshot, err := sbx.Pause(ctx, meta, sandbox.SnapshotUseCasePause, pauseOpts...)
	if err != nil {
		return nil, fmt.Errorf("error snapshotting sandbox: %w", err)
	}

	err = s.templateCache.AddSnapshot(
		ctx,
		meta.Template.BuildID,
		snapshot.MemorySnapshot.DiffHeader,
		snapshot.RootfsDiffHeader,
		snapshot.Snapfile,
		snapshot.Metafile,
		snapshot.MemorySnapshot.Diff,
		snapshot.RootfsDiff,
		snapshot.MemorySnapshot.ProvisionalDiffHeader,
		snapshot.MemorySnapshot.ProvisionalDiff,
		snapshot.MemorySnapshot.ProvisionalSwapDone,
	)
	if err != nil {
		return nil, fmt.Errorf("error adding snapshot to template cache: %w", err)
	}

	// Caller-supplied provenance (e.g. template_id) is forwarded as-is; team and
	// origin are orchestrator-authoritative and set last so they always win.
	objectMetadata := storage.ObjectMetadata{}
	maps.Copy(objectMetadata, provenance)
	objectMetadata[storage.ObjectMetadataTeamID] = sbx.Runtime.TeamID
	objectMetadata[storage.ObjectMetadataBuildOrigin] = string(buildOrigin)

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

		// Only advertise the build as fully uploaded when it actually landed.
		// On abandon/failure the bytes are not in storage, so marking it would
		// make chunk-serving falsely report "already uploaded".
		if uploadErr == nil {
			s.uploadedBuilds.Set(meta.Template.BuildID, struct{}{}, ttlcache.DefaultTTL)
		}

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
		meta:               meta,
		schedulingMetadata: snapshot.SchedulingMetadata,
		upload:             upload,
		completeUpload:     completeUpload,
		objectMetadata:     objectMetadata,
		filesystemOnly:     filesystemOnly,
		rootfsDiff:         snapshot.RootfsDiff,
	}, nil
}

// uploadSnapshotAsync uploads snapshot files to remote storage in the
// background and cleans up the Redis peer key once done. Used by the Pause
// handler where no prefetch data is available.
func (s *Server) uploadSnapshotAsync(ctx context.Context, sbx *sandbox.Sandbox, res *snapshotResult) {
	// Detach from the request: the upload retries for up to uploadTotalBudget.
	// A graceful shutdown waits for it to finish (see Server.Close via uploadsWG)
	// rather than cancelling, so an in-flight snapshot isn't dropped on restart.
	uploadCtx := context.WithoutCancel(ctx)

	s.uploadsInFlight.Add(1)
	s.uploadsWG.Go(func() {
		defer s.uploadsInFlight.Add(-1)

		spanCtx, span := tracer.Start(uploadCtx, "upload snapshot")
		defer span.End()

		err := retry.Do(
			spanCtx,
			defaultUploadRetryPolicy(),
			isRetryableUploadErr,
			res.upload.Run,
			func(attempt int, backoff time.Duration, err error) {
				sbxlogger.I(sbx).Warn(spanCtx, "snapshot upload attempt failed, retrying",
					zap.Int("attempt", attempt),
					zap.Duration("backoff", backoff),
					zap.Error(err),
				)
			},
		)
		if err != nil {
			sbxlogger.I(sbx).Error(spanCtx, "snapshot upload did not durably land", zap.Error(err))
			s.uploadFailedCounter.Add(spanCtx, 1, metric.WithAttributes(attribute.Bool("fs_only", res.filesystemOnly)))
		} else {
			sbxlogger.I(sbx).Info(spanCtx, "snapshot finished uploading successfully")
		}

		res.completeUpload(spanCtx, err)
	})
}

// setupSandboxLifecycle sets up the cleanup goroutine for a sandbox.
// markSandboxLive promotes a resumed sandbox to the live registry and starts its
// health checks. It is the counterpart to WithDeferredLiveRegistration (resume)
// and RebootSandbox's deferMarkRunning: callers on the resume-time upgrade path
// resume with routing deferred and call this only after maybeUpgradeEnvd has
// completed its post-/init, so the sandbox never appears in routing during the
// upgrade's pre-init auth window. Idempotent — MarkRunning is InsertIfAbsent.
func (s *Server) markSandboxLive(ctx context.Context, sbx *sandbox.Sandbox) {
	s.sandboxFactory.Sandboxes.MarkRunning(ctx, sbx)

	go sbx.Checks.Start(context.WithoutCancel(ctx))
}

func (s *Server) setupSandboxLifecycle(ctx context.Context, sbx *sandbox.Sandbox) {
	go func() {
		ctx, childSpan := tracer.Start(context.WithoutCancel(ctx), "stop sandbox-lifecycle", trace.WithNewRoot())
		defer childSpan.End()

		waitErr := sbx.Wait(ctx)
		if waitErr != nil {
			sbxlogger.I(sbx).Error(ctx, "failed to wait for sandbox, cleaning up", zap.Error(waitErr))
		}

		sbx.SetStoppedAt(time.Now())

		// A guest that dies cleanly leaves no wait error, so the log above
		// misses it.
		if sbx.GetStopReason() == sandbox.StopReasonCrashed {
			sbxlogger.I(sbx).Error(ctx, "sandbox crashed", zap.Error(waitErr))
		}

		// Every ending — kill, pause, checkpoint hand-off, crash — passes here.
		s.recordExecutionDuration(ctx, sbx)

		cleanupErr := sbx.Close(ctx)
		if cleanupErr != nil {
			if errors.Is(cleanupErr, syscall.EIO) || errors.Is(cleanupErr, syscall.ENXIO) {
				// After a VM crash the NBD device is in error state. sync() on
				// /dev/nbdX returns EIO (device error) or ENXIO (device already
				// disconnected). Both are expected here — no data is lost because
				// the VM already exited. Log at Warn and continue cleanup.
				sbxlogger.I(sbx).Warn(ctx, "failed to flush sandbox device after VM crash (ignoring)", zap.Error(cleanupErr))
			} else {
				sbxlogger.I(sbx).Error(ctx, "failed to cleanup sandbox, will remove from cache", zap.Error(cleanupErr))
			}
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
	teamID, buildId, eventsTTLDays, eventData := s.prepareSandboxEventData(ctx, sbx)

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
			EventsTTLDays:      eventsTTLDays,
		},
	)
}

// maybeUpgradeEnvd is the orchestrator's resume-time envd live-upgrade trigger
// . At resume it asks EnvdUpgradeTargetFlag whether the sandbox's envd
// should be swapped for a newer node-local build and, if so, delivers that
// binary into the guest and triggers envd's same-PID self-upgrade so the
// workload is preserved.
//
// Fully best-effort: recover()-wrapped, bounded timeouts, and every failure is
// logged-and-swallowed so it can never disrupt the universal resume path. The
// flag fallback is "off", so with no LaunchDarkly (e.g. dev) this is inert.
//
// It returns whether an upgrade actually completed (for the resume-latency
// label) and emits the rollout metrics: orchestrator.envd.upgrade.attempts
// {result,from_version,to_version}, .duration{result}, and .gated{reason}.
func (s *Server) maybeUpgradeEnvd(ctx context.Context, sbx *sandbox.Sandbox) (upgraded bool, fatalErr error) {
	// Decide, label, and confirm against the version the running envd actually
	// reports (captured on the resume-path /init) — not the template built-with,
	// which never changes across live upgrades and would otherwise re-trigger the
	// handover on every resume. Fall back to built-with only when no live version
	// was captured (an envd too old to report it — which the gate then rejects).
	from := sbx.LiveEnvdVersion()
	if from == "" {
		from = sbx.Config.Envd.Version
	}

	var (
		attempted bool
		toVersion string
		result    = "success"
		start     = time.Now()
	)
	defer func() {
		if r := recover(); r != nil {
			sbxlogger.I(sbx).Error(ctx, "envd auto-upgrade panic (recovered)", zap.Any("panic", r))
			result = "panic"
			attempted = true
			upgraded = false
		}
		if attempted {
			s.envdUpgradeAttempts.Add(ctx, 1, metric.WithAttributes(
				attribute.String("result", result),
				attribute.String("from_version", from),
				attribute.String("to_version", toVersion),
			))
			s.envdUpgradeDuration.Record(ctx, time.Since(start).Milliseconds(),
				metric.WithAttributes(attribute.String("result", result)))
		}
	}()

	// Flag-driven resolver, keyed on the LIVE version. "" path => no upgrade, with
	// a reason: off / same_version are the expected per-resume no-op (a re-resume
	// of an already-upgraded sandbox), deliberately not counted as noise; the rest
	// (not_staged — e.g. a bad SHA / rubbish flag value, getversion_failed,
	// downgrade) are misconfigurations worth a counted, logged signal so a broken
	// target is distinguishable from "already current".
	path, tv, reason := featureflags.ResolveEnvdUpgrade(ctx, s.featureFlags, from, s.config.HostEnvdPath, buildenvd.GetEnvdVersion)
	toVersion = tv
	if path == "" {
		switch reason {
		case "off", "same_version":
			// expected no-op — not counted
		default:
			s.envdUpgradeGated.Add(ctx, 1, metric.WithAttributes(attribute.String("reason", reason)))
			sbxlogger.I(sbx).Warn(ctx, "envd auto-upgrade: target not resolved",
				zap.String("reason", reason), zap.String("from", from))
		}

		return false, nil
	}

	// The *running* envd must already have the /upgrade endpoint + handover code,
	// else the delivery POST would 404 or hang. Count the skip so a ramp can see
	// the gated population.
	if ok, err := utils.IsGTEVersion(from, utils.MinEnvdVersionForUpgrade); err != nil || !ok {
		s.envdUpgradeGated.Add(ctx, 1, metric.WithAttributes(attribute.String("reason", "old_envd")))

		return false, nil
	}

	attempted = true
	start = time.Now()

	upCtx, span := tracer.Start(ctx, "envd-upgrade", trace.WithAttributes(
		attribute.String("envd.from_version", from),
		attribute.String("envd.to_version", toVersion),
	))
	defer span.End()

	// Delivery and readiness get INDEPENDENT budgets, not a shared cap: a slow
	// binary upload must not eat into the time envd has to re-adopt and answer
	// /init (which would mislabel a would-be success as delivery_failed/not_ready).
	const (
		deliverTimeout = 30 * time.Second
		readyTimeout   = 15 * time.Second
	)

	sbxlogger.I(sbx).Info(upCtx, "envd auto-upgrade: delivering+triggering",
		zap.String("from", from), zap.String("to", toVersion), zap.String("path", path))

	// Stream the new binary over the authenticated /upgrade endpoint (delivery
	// + trigger in one call) and let envd same-PID re-exec into it. NB: not the
	// build-time /files CopyFile path — a live, post-/init sandbox rejects that.
	execConfirmed, err := sbx.CallEnvdUpgrade(upCtx, path, "/usr/bin/envd.next", deliverTimeout)
	if err != nil {
		result = "delivery_failed"
		span.RecordError(err)
		sbxlogger.I(sbx).Error(upCtx, "envd auto-upgrade: trigger failed", zap.Error(err))

		// Delivery/trigger failed BEFORE execve, so the old envd is still running
		// and serving — best-effort: let the resume proceed on the old version.
		return false, nil
	}
	// WaitForEnvd re-runs /init, which re-captures the now-running version.
	//
	// Detach from upCtx (WithoutCancel) but keep the bounded readyTimeout: the
	// exec is a fait accompli by this point, so if the parent resume is cancelled
	// in this window we must still drive /init to completion. Running it on the
	// cancellable parent would let an ambiguous cancellation skip /init yet still
	// return recoverably, publishing a promoted-but-uninitialized sandbox (the
	// exec'd envd never gets its auth/env restored). Version confirmation below
	// then correctly distinguishes an actual exec from an untouched old envd.
	readyCtx := context.WithoutCancel(upCtx)
	if err := sbx.WaitForEnvd(readyCtx, sandbox.StartTypeResume, readyTimeout); err != nil {
		result = "not_ready"
		span.RecordError(err)
		sbxlogger.I(sbx).Error(upCtx, "envd auto-upgrade: envd not ready after upgrade", zap.Error(err))

		if !execConfirmed {
			// The delivery deadline fired without a confirmed exec — envd may
			// still be mid-handover on the OLD binary, which will thaw and keep
			// serving. Don't tear down a recoverable sandbox: best-effort, let the
			// resume proceed (a false-positive brick is worse than a missed
			// upgrade).
			return false, nil
		}

		// The exec is confirmed (the old envd is gone) but the new envd never
		// completed /init — its access token is unrestored and the
		// WithAuthorization handover gate fail-closes every RPC. It can't be made
		// both usable and secure without /init, so fail the resume (unrecoverable)
		// rather than return a live-but-permanently-bricked sandbox.
		return false, fmt.Errorf("envd live-upgrade left the sandbox uninitialized (post-upgrade /init failed): %w", err)
	}

	// Confirm by ground truth: the running envd must now report the target
	// version. This is the arbiter — a transport quirk (e.g. a slow exec that
	// never answered) can't mislabel a non-swap as success.
	if now := sbx.LiveEnvdVersion(); now != toVersion {
		result = "version_mismatch"
		span.SetAttributes(attribute.String("envd.observed_version", now))
		sbxlogger.I(sbx).Error(upCtx, "envd auto-upgrade: version did not flip",
			zap.String("observed", now), zap.String("expected", toVersion))

		// /init succeeded (envd is initialized and serving), it just isn't the
		// expected version — usable, not bricked, so don't fail the resume.
		return false, nil
	}

	// The version flipped, but if the in-guest handover itself failed post-exec
	// (ResumeFromHandover errored/panicked, so the workload was never re-adopted —
	// orphaned and unreaped), the old envd is gone and the sandbox is broken. Fail
	// the resume so the caller tears it down rather than hand back a live-but-
	// broken sandbox that merely reports the target version.
	if h := sbx.HandoverResult(); h != nil && h.Failed {
		result = "handover_failed"
		span.SetAttributes(attribute.Bool("envd.handover.failed", true))
		sbxlogger.I(sbx).Error(upCtx, "envd auto-upgrade: in-guest handover failed post-exec; workload not re-adopted")

		return false, errors.New("envd live-upgrade handover failed post-exec (workload not re-adopted)")
	}

	span.SetAttributes(attribute.Bool("envd.upgraded", true))

	// Record the handover outcome the new envd reported on /init (fleet
	// visibility into what it re-adopted). Per item (proc|retained|watcher) as
	// ok/failed so failed/(ok+failed) is the handover error rate — a non-zero
	// failed means the swap dropped or degraded something (a lost watch, an
	// unrecoverable exit code, a bad process config), which envd otherwise only
	// logs in-guest.
	if h := sbx.HandoverResult(); h != nil {
		recordItem := func(item string, ok, failed int) {
			s.envdUpgradeHandover.Add(upCtx, int64(ok), metric.WithAttributes(
				attribute.String("item", item), attribute.String("result", "ok")))
			s.envdUpgradeHandover.Add(upCtx, int64(failed), metric.WithAttributes(
				attribute.String("item", item), attribute.String("result", "failed")))
		}
		recordItem("proc", h.Procs-h.ProcsFailed, h.ProcsFailed)
		recordItem("retained", h.Retained-h.RetainedFailed, h.RetainedFailed)
		recordItem("watcher", h.Watchers-h.WatchersFailed, h.WatchersFailed)

		span.SetAttributes(
			attribute.Int("envd.handover.procs", h.Procs),
			attribute.Int("envd.handover.procs_failed", h.ProcsFailed),
			attribute.Int("envd.handover.retained", h.Retained),
			attribute.Int("envd.handover.retained_failed", h.RetainedFailed),
			attribute.Int("envd.handover.watchers", h.Watchers),
			attribute.Int("envd.handover.watchers_failed", h.WatchersFailed),
		)
		sbxlogger.I(sbx).Info(upCtx, "envd auto-upgrade: complete",
			zap.String("to", toVersion),
			zap.Int("procs", h.Procs), zap.Int("procs_failed", h.ProcsFailed),
			zap.Int("retained", h.Retained), zap.Int("retained_failed", h.RetainedFailed),
			zap.Int("watchers", h.Watchers), zap.Int("watchers_failed", h.WatchersFailed))
	} else {
		sbxlogger.I(sbx).Info(upCtx, "envd auto-upgrade: complete", zap.String("to", toVersion))
	}

	return true, nil
}
