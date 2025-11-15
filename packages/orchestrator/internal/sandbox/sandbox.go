package sandbox

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/cfg"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/build"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/fc"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/network"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/rootfs"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/template"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/metadata"
	featureflags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

var (
	meter                        = otel.GetMeterProvider().Meter("orchestrator.internal.sandbox")
	envdInitCalls                = utils.Must(telemetry.GetCounter(meter, telemetry.EnvdInitCalls))
	waitForEnvdDurationHistogram = utils.Must(telemetry.GetHistogram(meter, telemetry.WaitForEnvdDurationHistogramName))
)

var httpClient = http.Client{
	Timeout: 10 * time.Second,
	Transport: otelhttp.NewTransport(
		http.DefaultTransport,
	),
}

type Config struct {
	// TODO: Remove when the rootfs path is constant.
	// Only used for v1 rootfs paths format.
	BaseTemplateID string

	Vcpu  int64
	RamMB int64

	// TotalDiskSizeMB optional, now used only for metrics.
	TotalDiskSizeMB int64
	HugePages       bool

	Network *orchestrator.SandboxNetworkConfig

	Envd EnvdMetadata
}

type EnvdMetadata struct {
	Vars           map[string]string
	DefaultUser    *string
	DefaultWorkdir *string
	AccessToken    *string
	Version        string
}

type RuntimeMetadata struct {
	TemplateID  string
	SandboxID   string
	ExecutionID string

	// TeamID optional, used only for logging
	TeamID string
}

type Resources struct {
	Slot   *network.Slot
	rootfs rootfs.Provider
	memory uffd.MemoryBackend
}

type internalConfig struct {
	EnvdInitRequestTimeout time.Duration
}

type Metadata struct {
	internalConfig internalConfig
	Config         Config
	Runtime        RuntimeMetadata

	StartedAt time.Time
	EndAt     time.Time
}

type Sandbox struct {
	*Resources
	*Metadata

	config  cfg.BuilderConfig
	files   *storage.SandboxFiles
	cleanup *Cleanup

	process *fc.Process

	Template template.Template

	Checks *Checks

	// Deprecated: to be removed in the future
	// It was used to store the config to allow API restarts
	APIStoredConfig *orchestrator.SandboxConfig

	exit *utils.ErrorOnce
}

func (s *Sandbox) LoggerMetadata() sbxlogger.SandboxMetadata {
	return sbxlogger.SandboxMetadata{
		SandboxID:  s.Runtime.SandboxID,
		TemplateID: s.Runtime.TemplateID,
		TeamID:     s.Runtime.TeamID,
	}
}

type networkSlotRes struct {
	slot *network.Slot
	err  error
}

// Usage: defer handleSpanError(span, &err)
func handleSpanError(span trace.Span, err *error) {
	defer span.End()
	if err != nil && *err != nil {
		span.RecordError(*err)
		span.SetStatus(codes.Error, (*err).Error())
	}
}

func startExecutionSpan(ctx context.Context) (context.Context, trace.Span) {
	parentSpan := trace.SpanFromContext(ctx)

	ctx = context.WithoutCancel(ctx)
	ctx, span := tracer.Start(ctx, "execute sandbox", //nolint:spancheck // this is still just a helper method
		trace.WithNewRoot(),
	)

	parentSpan.AddLink(trace.LinkFromContext(ctx))

	return ctx, span //nolint:spancheck // this is still just a helper method
}

func (s *Sandbox) Wait(ctx context.Context) error {
	return s.exit.WaitWithContext(ctx)
}

func (s *Sandbox) Close(ctx context.Context) error {
	err := s.cleanup.Run(ctx)
	if err != nil {
		return fmt.Errorf("failed to cleanup sandbox: %w", err)
	}

	return nil
}

// Stop kills the sandbox.
func (s *Sandbox) Stop(ctx context.Context) error {
	ctx, span := tracer.Start(ctx, "sandbox-close")
	defer span.End()

	var errs []error

	// Stop the health checks before stopping the sandbox
	s.Checks.Stop()

	fcStopErr := s.process.Stop(ctx)
	if fcStopErr != nil {
		errs = append(errs, fmt.Errorf("failed to stop FC: %w", fcStopErr))
	}

	// The process exited, we can continue with the rest of the cleanup.
	// We could use select with ctx.Done() to wait for cancellation, but if the process is not exited the whole cleanup will be in a bad state and will result in unexpected behavior.
	<-s.process.Exit.Done()

	uffdStopErr := s.Resources.memory.Stop()
	if uffdStopErr != nil {
		errs = append(errs, fmt.Errorf("failed to stop uffd: %w", uffdStopErr))
	}

	return errors.Join(errs...)
}

func (s *Sandbox) FirecrackerVersions() fc.FirecrackerVersions {
	return s.process.Versions
}

func (s *Sandbox) Pause(
	ctx context.Context,
	m metadata.Template,
) (*Snapshot, error) {
	ctx, span := tracer.Start(ctx, "sandbox-snapshot")
	defer span.End()

	snapshotTemplateFiles, err := m.Template.CacheFiles(s.config)
	if err != nil {
		return nil, fmt.Errorf("failed to get template files: %w", err)
	}

	buildID, err := uuid.Parse(snapshotTemplateFiles.BuildID)
	if err != nil {
		return nil, fmt.Errorf("failed to parse build id: %w", err)
	}

	// Stop the health check before pausing the VM
	s.Checks.Stop()

	if err := s.process.Pause(ctx); err != nil {
		return nil, fmt.Errorf("failed to pause VM: %w", err)
	}

	if err := s.memory.Disable(); err != nil {
		return nil, fmt.Errorf("failed to disable uffd: %w", err)
	}

	// Snapfile is not closed as it's returned and cached for later use (like resume)
	snapfile := template.NewLocalFileLink(snapshotTemplateFiles.CacheSnapfilePath(s.config))
	// Memfile is also closed on diff creation processing
	/* The process of snapshotting memory is as follows:
	1. Pause FC via API
	2. Snapshot FC via API—memory dump to “file on disk” that is actually tmpfs, because it is too slow
	3. Create the diff - copy the diff pages from tmpfs to normal disk file
	4. Delete tmpfs file
	5. Unlock so another snapshot can use tmpfs space
	*/
	memfile, err := storage.AcquireTmpMemfile(ctx, s.config, buildID.String())
	if err != nil {
		return nil, fmt.Errorf("failed to acquire memfile snapshot: %w", err)
	}
	// Close the file even if an error occurs
	defer memfile.Close()

	err = s.process.CreateSnapshot(
		ctx,
		snapfile.Path(),
		memfile.Path(),
	)
	if err != nil {
		return nil, fmt.Errorf("error creating snapshot: %w", err)
	}

	// Gather data for postprocessing
	originalMemfile, err := s.Template.Memfile(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get original memfile: %w", err)
	}
	originalRootfs, err := s.Template.Rootfs()
	if err != nil {
		return nil, fmt.Errorf("failed to get original rootfs: %w", err)
	}

	// Start POSTPROCESSING
	memfileDiff, memfileDiffHeader, err := pauseProcessMemory(
		ctx,
		buildID,
		originalMemfile.Header(),
		&MemoryDiffCreator{
			memfile:    memfile,
			dirtyPages: s.memory.Dirty(),
			blockSize:  originalMemfile.BlockSize(),
			doneHook: func(context.Context) error {
				return memfile.Close()
			},
		},
		s.config.DefaultCacheDir,
	)
	if err != nil {
		return nil, fmt.Errorf("error while post processing: %w", err)
	}

	rootfsDiff, rootfsDiffHeader, err := pauseProcessRootfs(
		ctx,
		buildID,
		originalRootfs.Header(),
		&RootfsDiffCreator{
			rootfs:    s.rootfs,
			closeHook: s.Close,
		},
		s.config.DefaultCacheDir,
	)
	if err != nil {
		return nil, fmt.Errorf("error while post processing: %w", err)
	}

	metadataFileLink := template.NewLocalFileLink(snapshotTemplateFiles.CacheMetadataPath(s.config))
	err = m.ToFile(metadataFileLink.Path())
	if err != nil {
		return nil, err
	}

	return &Snapshot{
		Snapfile:          snapfile,
		Metafile:          metadataFileLink,
		MemfileDiff:       memfileDiff,
		MemfileDiffHeader: memfileDiffHeader,
		RootfsDiff:        rootfsDiff,
		RootfsDiffHeader:  rootfsDiffHeader,
	}, nil
}

func pauseProcessMemory(
	ctx context.Context,
	buildId uuid.UUID,
	originalHeader *header.Header,
	diffCreator DiffCreator,
	cacheDir string,
) (build.Diff, *header.Header, error) {
	ctx, span := tracer.Start(ctx, "process-memory")
	defer span.End()

	memfileDiffFile, err := build.NewLocalDiffFile(
		cacheDir,
		buildId.String(),
		build.Memfile,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create memfile diff file: %w", err)
	}

	m, err := diffCreator.process(ctx, memfileDiffFile)
	if err != nil {
		return nil, nil, fmt.Errorf("error creating diff: %w", err)
	}
	telemetry.ReportEvent(ctx, "created diff")

	memfileMapping, err := m.CreateMapping(ctx, buildId)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create memfile mapping: %w", err)
	}

	memfileMappings := header.MergeMappings(
		originalHeader.Mapping,
		memfileMapping,
	)
	// TODO: We can run normalization only when empty mappings are not empty for this snapshot
	memfileMappings = header.NormalizeMappings(memfileMappings)
	telemetry.ReportEvent(ctx, "merged memfile mappings")

	memfileDiff, err := memfileDiffFile.CloseToDiff(int64(originalHeader.Metadata.BlockSize))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to convert memfile diff file to local diff: %w", err)
	}

	telemetry.ReportEvent(ctx, "converted memfile diff file to local diff")

	memfileMetadata := originalHeader.Metadata.NextGeneration(buildId)

	telemetry.SetAttributes(ctx,
		attribute.Int64("snapshot.memfile.header.mappings.length", int64(len(memfileMappings))),
		attribute.Int64("snapshot.memfile.diff.size", int64(m.Dirty.Count()*uint(originalHeader.Metadata.BlockSize))),
		attribute.Int64("snapshot.memfile.mapped_size", int64(memfileMetadata.Size)),
		attribute.Int64("snapshot.memfile.block_size", int64(memfileMetadata.BlockSize)),
		attribute.Int64("snapshot.metadata.version", int64(memfileMetadata.Version)),
		attribute.Int64("snapshot.metadata.generation", int64(memfileMetadata.Generation)),
		attribute.String("snapshot.metadata.build_id", memfileMetadata.BuildId.String()),
		attribute.String("snapshot.metadata.base_build_id", memfileMetadata.BaseBuildId.String()),
	)

	memfileHeader, err := header.NewHeader(memfileMetadata, memfileMappings)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create memfile header: %w", err)
	}

	err = header.ValidateMappings(memfileHeader.Mapping, memfileHeader.Metadata.Size, memfileHeader.Metadata.BlockSize)
	if err != nil {
		if memfileHeader.IsNormalizeFixApplied() {
			return nil, nil, fmt.Errorf("invalid memfile header mappings: %w", err)
		}

		zap.L().Warn("memfile header mappings are invalid, but normalize fix is not applied", zap.Error(err), logger.WithBuildID(memfileHeader.Metadata.BuildId.String()))
	}

	return memfileDiff, memfileHeader, nil
}

func pauseProcessRootfs(
	ctx context.Context,
	buildId uuid.UUID,
	originalHeader *header.Header,
	diffCreator DiffCreator,
	cacheDir string,
) (build.Diff, *header.Header, error) {
	ctx, span := tracer.Start(ctx, "process-rootfs")
	defer span.End()

	rootfsDiffFile, err := build.NewLocalDiffFile(cacheDir, buildId.String(), build.Rootfs)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create rootfs diff: %w", err)
	}

	rootfsDiffMetadata, err := diffCreator.process(ctx, rootfsDiffFile)
	if err != nil {
		return nil, nil, fmt.Errorf("error creating diff: %w", err)
	}

	telemetry.ReportEvent(ctx, "exported rootfs")
	rootfsMapping, err := rootfsDiffMetadata.CreateMapping(ctx, buildId)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create rootfs diff: %w", err)
	}

	rootfsMappings := header.MergeMappings(
		originalHeader.Mapping,
		rootfsMapping,
	)
	// TODO: We can run normalization only when empty mappings are not empty for this snapshot
	rootfsMappings = header.NormalizeMappings(rootfsMappings)
	telemetry.ReportEvent(ctx, "merged rootfs mappings")

	rootfsDiff, err := rootfsDiffFile.CloseToDiff(int64(originalHeader.Metadata.BlockSize))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to convert rootfs diff file to local diff: %w", err)
	}
	telemetry.ReportEvent(ctx, "converted rootfs diff file to local diff")

	rootfsMetadata := originalHeader.Metadata.NextGeneration(buildId)

	telemetry.SetAttributes(ctx,
		attribute.Int64("snapshot.rootfs.header.mappings.length", int64(len(rootfsMappings))),
		attribute.Int64("snapshot.rootfs.diff.size", int64(rootfsDiffMetadata.Dirty.Count()*uint(originalHeader.Metadata.BlockSize))),
		attribute.Int64("snapshot.rootfs.mapped_size", int64(rootfsMetadata.Size)),
		attribute.Int64("snapshot.rootfs.block_size", int64(rootfsMetadata.BlockSize)),
	)

	rootfsHeader, err := header.NewHeader(rootfsMetadata, rootfsMappings)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create rootfs header: %w", err)
	}

	err = header.ValidateMappings(rootfsHeader.Mapping, rootfsHeader.Metadata.Size, rootfsHeader.Metadata.BlockSize)
	if err != nil {
		if rootfsHeader.IsNormalizeFixApplied() {
			return nil, nil, fmt.Errorf("invalid rootfs header mappings: %w", err)
		}

		zap.L().Warn("rootfs header mappings are invalid, but normalize fix is not applied", zap.Error(err), logger.WithBuildID(rootfsHeader.Metadata.BuildId.String()))
	}

	return rootfsDiff, rootfsHeader, nil
}

func getNetworkSlotAsync(
	ctx context.Context,
	networkPool *network.Pool,
	cleanup *Cleanup,
	network *orchestrator.SandboxNetworkConfig,
) chan networkSlotRes {
	ctx, span := tracer.Start(ctx, "get-network-slot")
	defer span.End()

	r := make(chan networkSlotRes, 1)

	go func() {
		defer close(r)

		ips, err := networkPool.Get(ctx, network)
		if err != nil {
			r <- networkSlotRes{nil, fmt.Errorf("failed to get network slot: %w", err)}

			return
		}

		cleanup.Add(func(ctx context.Context) error {
			_, span := tracer.Start(ctx, "network-slot-clean")
			defer span.End()

			// We can run this cleanup asynchronously, as it is not important for the sandbox lifecycle
			go func(ctx context.Context) {
				returnErr := networkPool.Return(ctx, ips)
				if returnErr != nil {
					zap.L().Error("failed to return network slot", zap.Error(returnErr))
				}
			}(context.WithoutCancel(ctx))

			return nil
		})

		r <- networkSlotRes{ips, nil}
	}()

	return r
}

func serveMemory(
	ctx context.Context,
	cleanup *Cleanup,
	memfile block.ReadonlyDevice,
	socketPath, sandboxID string,
) (uffd.MemoryBackend, error) {
	ctx, span := tracer.Start(ctx, "serve-memory")
	defer span.End()

	fcUffd, err := uffd.New(memfile, socketPath, memfile.BlockSize())
	if err != nil {
		return nil, fmt.Errorf("failed to create uffd: %w", err)
	}

	telemetry.ReportEvent(ctx, "created uffd")

	if err = fcUffd.Start(ctx, sandboxID); err != nil {
		return nil, fmt.Errorf("failed to start uffd: %w", err)
	}

	telemetry.ReportEvent(ctx, "started uffd")

	cleanup.Add(func(ctx context.Context) error {
		_, span := tracer.Start(ctx, "uffd-stop")
		defer span.End()

		if err := fcUffd.Stop(); err != nil {
			return fmt.Errorf("failed to stop uffd: %w", err)
		}

		return nil
	})

	return fcUffd, nil
}

func (s *Sandbox) WaitForExit(ctx context.Context) error {
	ctx, span := tracer.Start(ctx, "sandbox-wait-for-exit")
	defer span.End()

	timeout := time.Until(s.EndAt)

	select {
	case <-time.After(timeout):
		return fmt.Errorf("waiting for exit took too long")
	case <-ctx.Done():
		return nil
	case <-s.exit.Done():
		err := s.exit.Error()
		if err == nil {
			return nil
		}

		return fmt.Errorf("fc process exited prematurely: %w", err)
	}
}

func (s *Sandbox) WaitForEnvd(
	ctx context.Context,
	timeout time.Duration,
) (e error) {
	start := time.Now()
	ctx, span := tracer.Start(ctx, "sandbox-wait-for-start")
	defer span.End()

	defer func() {
		if e != nil {
			return
		}
		duration := time.Since(start).Milliseconds()
		waitForEnvdDurationHistogram.Record(ctx, duration, metric.WithAttributes(
			telemetry.WithEnvdVersion(s.Config.Envd.Version),
			attribute.Int64("timeout_ms", s.internalConfig.EnvdInitRequestTimeout.Milliseconds()),
		))
		// Update the sandbox as started now
		s.Metadata.StartedAt = time.Now()
	}()
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)

	go func() {
		select {
		// Ensure the syncing takes at most timeout seconds.
		case <-time.After(timeout):
			cancel(fmt.Errorf("syncing took too long"))
		case <-ctx.Done():
			return
		case <-s.process.Exit.Done():
			err := s.process.Exit.Error()

			cancel(fmt.Errorf("fc process exited prematurely: %w", err))
		}
	}()

	if err := s.initEnvd(ctx); err != nil {
		return fmt.Errorf("failed to init new envd: %w", err)
	}

	telemetry.ReportEvent(ctx, fmt.Sprintf("[sandbox %s]: initialized new envd", s.Metadata.Runtime.SandboxID))

	return nil
}

func (f *Factory) GetEnvdInitRequestTimeout(ctx context.Context) time.Duration {
	envdInitRequestTimeoutMs, err := f.featureFlags.IntFlag(ctx, featureflags.EnvdInitTimeoutSeconds)
	if err != nil {
		zap.L().Warn("failed to get envd timeout from feature flag, using default", zap.Error(err))
	}

	return time.Duration(envdInitRequestTimeoutMs) * time.Millisecond
}
