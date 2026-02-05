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
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/nbd"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/network"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/rootfs"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/template"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd/prefetch"
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

var SandboxHttpTransport = otelhttp.NewTransport(
	&http.Transport{
		DisableKeepAlives: true,
		ForceAttemptHTTP2: false,
	},
)

// Http client that should be used for requests to sandboxes.
var sandboxHttpClient = http.Client{
	Timeout:   10 * time.Second,
	Transport: SandboxHttpTransport,
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

	FirecrackerConfig fc.Config
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
	// Original TTL configured by the API.
	TimeoutSeconds *int32
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

	stop utils.Lazy[error]
}

func (s *Sandbox) LoggerMetadata() sbxlogger.SandboxMetadata {
	return sbxlogger.SandboxMetadata{
		SandboxID:  s.Runtime.SandboxID,
		TemplateID: s.Runtime.TemplateID,
		TeamID:     s.Runtime.TeamID,
	}
}

type Factory struct {
	config       cfg.BuilderConfig
	networkPool  *network.Pool
	devicePool   *nbd.DevicePool
	featureFlags *featureflags.Client
}

func NewFactory(
	config cfg.BuilderConfig,
	networkPool *network.Pool,
	devicePool *nbd.DevicePool,
	featureFlags *featureflags.Client,
) *Factory {
	return &Factory{
		config:       config,
		networkPool:  networkPool,
		devicePool:   devicePool,
		featureFlags: featureFlags,
	}
}

// CreateSandbox creates the sandbox.
// IMPORTANT: You must Close() the sandbox after you are done with it.
func (f *Factory) CreateSandbox(
	ctx context.Context,
	config Config,
	runtime RuntimeMetadata,
	template template.Template,
	sandboxTimeout time.Duration,
	rootfsCachePath string,
	processOptions fc.ProcessOptions,
	apiConfigToStore *orchestrator.SandboxConfig,
) (s *Sandbox, e error) {
	ctx, span := tracer.Start(ctx, "create sandbox")
	defer span.End()
	defer handleSpanError(span, &e)

	execCtx, execSpan := startExecutionSpan(ctx)

	exit := utils.NewErrorOnce()

	cleanup := NewCleanup()
	defer func() {
		if e != nil {
			cleanupErr := cleanup.Run(ctx)
			e = errors.Join(e, cleanupErr)
			handleSpanError(execSpan, &e)
			execSpan.End()
		}
	}()

	ipsPromise := getNetworkSlot(ctx, f.networkPool, cleanup, config.Network)

	sandboxFiles := template.Files().NewSandboxFiles(runtime.SandboxID)
	cleanup.Add(ctx, cleanupFiles(f.config, sandboxFiles))

	rootFS, err := template.Rootfs()
	if err != nil {
		return nil, fmt.Errorf("failed to get rootfs: %w", err)
	}

	var rootfsProvider rootfs.Provider
	if rootfsCachePath == "" {
		rootfsProvider, err = rootfs.NewNBDProvider(
			ctx,
			rootFS,
			sandboxFiles.SandboxCacheRootfsPath(f.config.StorageConfig),
			f.devicePool,
			f.featureFlags,
		)
	} else {
		rootfsProvider, err = rootfs.NewDirectProvider(
			ctx,
			rootFS,
			// Populate direct cache directly from the source file
			// This is needed for marking all blocks as dirty and being able to read them directly
			rootfsCachePath,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to create rootfs overlay: %w", err)
	}
	cleanup.Add(ctx, rootfsProvider.Close)
	go func() {
		runErr := rootfsProvider.Start(execCtx)
		if runErr != nil {
			logger.L().Error(ctx, "rootfs overlay error", zap.Error(runErr))
		}
	}()

	memfile, err := template.Memfile(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get memfile: %w", err)
	}

	memfileSize, err := memfile.Size(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get memfile size: %w", err)
	}

	// / ==== END of resources initialization ====
	ips, err := ipsPromise.Wait(ctx)
	if err != nil {
		return nil, err
	}

	fcHandle, err := fc.NewProcess(
		ctx,
		execCtx,
		f.config,
		ips,
		sandboxFiles,
		config.FirecrackerConfig,
		rootfsProvider,
		fc.ConstantRootfsPaths,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to init FC: %w", err)
	}

	telemetry.ReportEvent(ctx, "created fc client")

	err = fcHandle.Create(
		ctx,
		sbxlogger.SandboxMetadata{
			SandboxID:  runtime.SandboxID,
			TemplateID: runtime.TemplateID,
			TeamID:     runtime.TeamID,
		},
		config.Vcpu,
		config.RamMB,
		config.HugePages,
		processOptions,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create FC: %w", err)
	}
	telemetry.ReportEvent(ctx, "created fc process")

	resources := &Resources{
		Slot:   ips,
		rootfs: rootfsProvider,
		memory: uffd.NewNoopMemory(memfileSize, memfile.BlockSize(), fcHandle.MemoryInfo),
	}

	var timeoutSeconds *int32
	if apiConfigToStore != nil && apiConfigToStore.SandboxTimeoutSeconds != nil {
		value := *apiConfigToStore.SandboxTimeoutSeconds
		timeoutSeconds = &value
	}

	metadata := &Metadata{
		internalConfig: internalConfig{
			EnvdInitRequestTimeout: f.GetEnvdInitRequestTimeout(ctx),
		},

		Config:  config,
		Runtime: runtime,

		StartedAt:      time.Now(),
		EndAt:          time.Now().Add(sandboxTimeout),
		TimeoutSeconds: timeoutSeconds,
	}

	sbx := &Sandbox{
		Resources: resources,
		Metadata:  metadata,

		Template: template,
		config:   f.config,
		files:    sandboxFiles,
		process:  fcHandle,

		cleanup: cleanup,

		APIStoredConfig: apiConfigToStore,

		exit: exit,
	}

	sbx.Checks = NewChecks(sbx, false)

	// Stop the sandbox first if it is still running, otherwise do nothing
	cleanup.AddPriority(ctx, sbx.Stop)

	go func() {
		defer execSpan.End()

		ctx, span := tracer.Start(execCtx, "sandbox-exit-wait")
		defer span.End()

		// If the process exists, stop the sandbox properly
		fcErr := fcHandle.Exit.Wait()
		err := sbx.Stop(ctx)

		exit.SetError(errors.Join(err, fcErr))
	}()

	return sbx, nil
}

// Usage: defer handleSpanError(span, &err)
func handleSpanError(span trace.Span, err *error) {
	defer span.End()
	if err != nil && *err != nil {
		span.RecordError(*err)
		span.SetStatus(codes.Error, (*err).Error())
	}
}

// ResumeSandbox resumes the sandbox from already saved template or snapshot.
// IMPORTANT: You must Close() the sandbox after you are done with it.
func (f *Factory) ResumeSandbox(
	ctx context.Context,
	t template.Template,
	config Config,
	runtime RuntimeMetadata,
	startedAt time.Time,
	endAt time.Time,
	apiConfigToStore *orchestrator.SandboxConfig,
) (s *Sandbox, e error) {
	ctx, span := tracer.Start(ctx, "resume sandbox")
	defer span.End()
	defer handleSpanError(span, &e)

	execCtx, execSpan := startExecutionSpan(ctx)

	exit := utils.NewErrorOnce()

	cleanup := NewCleanup()
	defer func() {
		if e != nil {
			cleanupErr := cleanup.Run(ctx)
			e = errors.Join(e, cleanupErr)
			handleSpanError(execSpan, &e)
			execSpan.End()
		}
	}()

	sandboxFiles := t.Files().NewSandboxFiles(runtime.SandboxID)
	cleanup.Add(ctx, cleanupFiles(f.config, sandboxFiles))

	telemetry.ReportEvent(ctx, "created sandbox files")

	// Uffd initialization
	fcUffdPath := sandboxFiles.SandboxUffdSocketPath()
	uffdPromise := utils.NewPromise(func() (*uffd.Uffd, error) {
		memfile, err := t.Memfile(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get memfile: %w", err)
		}

		telemetry.ReportEvent(ctx, "got template memfile")

		return uffd.New(memfile, fcUffdPath), nil
	})

	// Prefetching
	go func() {
		memfile, err := t.Memfile(ctx)
		if err != nil {
			return
		}

		meta, err := t.Metadata()
		if err != nil {
			return
		}

		telemetry.ReportEvent(ctx, "got metadata")

		// Start background prefetcher as early as possible if prefetch mapping exists
		// Fetching from source starts immediately; copying waits for uffd to be ready
		if meta.Prefetch != nil && meta.Prefetch.Memory != nil {
			fcUffd, err := uffdPromise.Wait(ctx)
			if err != nil {
				return
			}

			telemetry.ReportEvent(ctx, "starting prefetcher")
			l := logger.L().With(logger.WithSandboxID(runtime.SandboxID), logger.WithTemplateID(runtime.TemplateID), logger.WithTeamID(runtime.TeamID))

			go func() {
				p := prefetch.New(
					l,
					memfile,
					fcUffd,
					meta.Prefetch.Memory,
					f.featureFlags,
				)
				err := p.Start(execCtx)
				if err != nil {
					l.Error(ctx, "failed to start prefetcher", zap.Error(err))
				}
			}()
		}
	}()

	// Slot initialization
	ipsPromise := getNetworkSlot(ctx, f.networkPool, cleanup, config.Network)

	// Rootfs initialization
	overlayPromise := utils.NewPromise(func() (rootfs.Provider, error) {
		readonlyRootfs, err := t.Rootfs()
		if err != nil {
			return nil, fmt.Errorf("failed to get rootfs: %w", err)
		}

		telemetry.ReportEvent(ctx, "got template rootfs")

		overlay, err := rootfs.NewNBDProvider(
			ctx,
			readonlyRootfs,
			sandboxFiles.SandboxCacheRootfsPath(f.config.StorageConfig),
			f.devicePool,
			f.featureFlags,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to create rootfs overlay: %w", err)
		}

		cleanup.Add(ctx, overlay.Close)

		telemetry.ReportEvent(ctx, "created rootfs overlay")

		go func() {
			runErr := overlay.Start(execCtx)
			if runErr != nil {
				logger.L().Error(ctx, "rootfs overlay error", zap.Error(runErr))
			}
		}()

		return overlay, nil
	})

	// Memory initialization
	memoryPromise := utils.NewPromise(func() (struct{}, error) {
		fcUffd, err := uffdPromise.Wait(ctx)
		if err != nil {
			return struct{}{}, err
		}

		err = serveMemory(
			execCtx,
			cleanup,
			fcUffd,
			runtime.SandboxID,
		)
		if err != nil {
			return struct{}{}, fmt.Errorf("failed to serve memory: %w", err)
		}

		telemetry.ReportEvent(ctx, "started serving memory")

		return struct{}{}, nil
	})

	// Wait for all resources to be initialized
	ips, err := ipsPromise.Wait(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get network slot: %w", err)
	}

	telemetry.ReportEvent(ctx, "got network slot")

	overlay, err := overlayPromise.Wait(ctx)
	if err != nil {
		return nil, err
	}

	_, err = memoryPromise.Wait(ctx)
	if err != nil {
		return nil, err
	}
	// ==== END of resources initialization ====

	rootfs, err := t.Rootfs()
	if err != nil {
		return nil, fmt.Errorf("failed to get rootfs overlay: %w", err)
	}

	meta, err := t.Metadata()
	if err != nil {
		return nil, fmt.Errorf("failed to get metadata: %w", err)
	}

	fcHandle, fcErr := fc.NewProcess(
		ctx,
		execCtx,
		f.config,
		ips,
		sandboxFiles,
		// The versions need to base exactly the same as the paused sandbox template because of the FC compatibility.
		config.FirecrackerConfig,
		overlay,
		fc.RootfsPaths{
			TemplateVersion: meta.Version,
			TemplateID:      config.BaseTemplateID,
			BuildID:         rootfs.Header().Metadata.BaseBuildId.String(),
		},
	)
	if fcErr != nil {
		return nil, fmt.Errorf("failed to create FC: %w", fcErr)
	}

	telemetry.ReportEvent(ctx, "created FC process")

	// todo: check if kernel, firecracker, and envd versions exist
	snapfile, err := t.Snapfile()
	if err != nil {
		return nil, fmt.Errorf("failed to get snapfile: %w", err)
	}

	telemetry.ReportEvent(ctx, "got snapfile")

	fcUffd, err := uffdPromise.Wait(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get uffd: %w", err)
	}

	uffdStartCtx, cancelUffdStartCtx := context.WithCancelCause(ctx)
	defer cancelUffdStartCtx(fmt.Errorf("uffd finished starting"))
	go func() {
		uffdWaitErr := fcUffd.Exit().Wait()

		cancelUffdStartCtx(fmt.Errorf("uffd process exited: %w", errors.Join(uffdWaitErr, context.Cause(uffdStartCtx))))
	}()
	fcStartErr := fcHandle.Resume(
		uffdStartCtx,
		sbxlogger.SandboxMetadata{
			SandboxID:  runtime.SandboxID,
			TemplateID: runtime.TemplateID,
			TeamID:     runtime.TeamID,
		},
		fcUffdPath,
		snapfile,
		fcUffd.Ready(),
		ips,
		config.Envd.AccessToken,
	)
	if fcStartErr != nil {
		return nil, fmt.Errorf("failed to start FC: %w", fcStartErr)
	}

	telemetry.ReportEvent(ctx, "initialized FC")

	resources := &Resources{
		Slot:   ips,
		rootfs: overlay,
		memory: fcUffd,
	}

	var timeoutSeconds *int32
	if apiConfigToStore != nil && apiConfigToStore.SandboxTimeoutSeconds != nil {
		value := *apiConfigToStore.SandboxTimeoutSeconds
		timeoutSeconds = &value
	}

	metadata := &Metadata{
		internalConfig: internalConfig{
			EnvdInitRequestTimeout: f.GetEnvdInitRequestTimeout(ctx),
		},

		Config:  config,
		Runtime: runtime,

		StartedAt:      startedAt,
		EndAt:          endAt,
		TimeoutSeconds: timeoutSeconds,
	}

	sbx := &Sandbox{
		Resources: resources,
		Metadata:  metadata,

		Template: t,
		config:   f.config,
		files:    sandboxFiles,
		process:  fcHandle,

		cleanup: cleanup,

		APIStoredConfig: apiConfigToStore,

		exit: exit,
	}

	useClickhouseMetrics := f.featureFlags.BoolFlag(ctx, featureflags.MetricsWriteFlag)

	// Part of the sandbox as we need to stop Checks before pausing the sandbox
	// This is to prevent race condition of reporting unhealthy sandbox
	sbx.Checks = NewChecks(sbx, useClickhouseMetrics)

	cleanup.AddPriority(ctx, func(ctx context.Context) error {
		// Stop the sandbox first if it is still running, otherwise do nothing
		return sbx.Stop(ctx)
	})

	telemetry.ReportEvent(execCtx, "waiting for envd")

	err = sbx.WaitForEnvd(
		ctx,
		f.config.EnvdTimeout,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to wait for sandbox start: %w", err)
	}

	telemetry.ReportEvent(execCtx, "envd initialized")

	go sbx.Checks.Start(execCtx)

	go func() {
		defer execSpan.End()

		ctx, span := tracer.Start(execCtx, "sandbox-exit-wait")
		defer span.End()

		// Wait for either uffd or fc process to exit
		select {
		case <-fcUffd.Exit().Done():
		case <-fcHandle.Exit.Done():
		}

		err := sbx.Stop(ctx)

		uffdWaitErr := fcUffd.Exit().Wait()
		fcErr := fcHandle.Exit.Wait()
		exit.SetError(errors.Join(err, fcErr, uffdWaitErr))
	}()

	return sbx, nil
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

// Stop kills the sandbox. It is safe to call multiple times; only the first
// call will actually perform the stop operation.
func (s *Sandbox) Stop(ctx context.Context) error {
	return s.stop.GetOrInit(func() error {
		return s.doStop(ctx)
	})
}

// doStop performs the actual stop operation.
func (s *Sandbox) doStop(ctx context.Context) error {
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

func (s *Sandbox) Shutdown(ctx context.Context) error {
	ctx, span := tracer.Start(ctx, "shutdown sandbox")
	defer span.End()

	// Stop the health check before pausing the VM
	s.Checks.Stop()

	if err := s.process.Pause(ctx); err != nil {
		return fmt.Errorf("failed to pause VM: %w", err)
	}

	// This is required because the FC API doesn't support passing /dev/null
	tf, err := storage.TemplateFiles{
		BuildID: uuid.New().String(),
	}.CacheFiles(s.config.StorageConfig)
	if err != nil {
		return fmt.Errorf("failed to create template files: %w", err)
	}
	defer tf.Close()

	// The snapfile is required only because the FC API doesn't support passing /dev/null
	snapfile := template.NewLocalFileLink(tf.CacheSnapfilePath())
	defer snapfile.Close()

	err = s.process.CreateSnapshot(ctx, snapfile.Path())
	if err != nil {
		return fmt.Errorf("error creating snapshot: %w", err)
	}

	// This should properly flush rootfs to the underlying device.
	err = s.Close(ctx)
	if err != nil {
		return fmt.Errorf("error stopping sandbox: %w", err)
	}

	return nil
}

// Pause creates a snapshot of the sandbox.
//
// Currently the memory snapshotting works like this:
//  1. We pause FC VM
//  2. We call FC snapshot endpoint without specifying memfile path. With our custom FC,
//     this only creates the snapfile and drains and flushes the disk.
//  3. We call custom FC endpoint that returns memory addresses of the sandbox memory, that we will process after.
//  4. In case of NoopMemory (the sandbox was not a resume) we also call the custom FC endpoint,
//     that returns info about resident memory pages and about empty memory pages.
//  5. Base on the info from the custom FC endpoint or from Uffd we copy the pages directly from the FC process to a local cache.
//  6. We then can either close the sandbox or resume it.
func (s *Sandbox) Pause(
	ctx context.Context,
	m metadata.Template,
) (st *Snapshot, e error) {
	ctx, span := tracer.Start(ctx, "sandbox-snapshot")
	defer span.End()

	cleanup := NewCleanup()
	defer func() {
		// Cleanup the snapshot if an error occurs
		if e != nil {
			err := cleanup.Run(ctx)
			e = errors.Join(e, err)
		}
	}()

	snapshotTemplateFiles, err := storage.TemplateFiles{BuildID: m.Template.BuildID}.CacheFiles(s.config.StorageConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to get template files: %w", err)
	}
	cleanup.AddNoContext(ctx, snapshotTemplateFiles.Close)

	buildID, err := uuid.Parse(snapshotTemplateFiles.BuildID)
	if err != nil {
		return nil, fmt.Errorf("failed to parse build id: %w", err)
	}

	// Stop the health check before pausing the VM
	s.Checks.Stop()

	if err := s.process.Pause(ctx); err != nil {
		return nil, fmt.Errorf("failed to pause VM: %w", err)
	}

	// Snapfile is not closed as it's returned and cached for later use (like resume)
	snapfile := template.NewLocalFileLink(snapshotTemplateFiles.CacheSnapfilePath())
	cleanup.AddNoContext(ctx, snapfile.Close)

	err = s.process.CreateSnapshot(ctx, snapfile.Path())
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

	memfileDiffMetadata, err := s.Resources.memory.DiffMetadata(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get memfile metadata: %w", err)
	}

	// Start POSTPROCESSING
	memfileDiff, memfileDiffHeader, err := pauseProcessMemory(
		ctx,
		buildID,
		originalMemfile.Header(),
		memfileDiffMetadata,
		s.config.DefaultCacheDir,
		s.process,
	)
	if err != nil {
		return nil, fmt.Errorf("error while post processing: %w", err)
	}
	cleanup.AddNoContext(ctx, memfileDiff.Close)

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
	cleanup.AddNoContext(ctx, rootfsDiff.Close)

	metadataFileLink := template.NewLocalFileLink(snapshotTemplateFiles.CacheMetadataPath())
	cleanup.AddNoContext(ctx, metadataFileLink.Close)

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

		cleanup: cleanup,
	}, nil
}

// MemoryPrefetchData returns the ordered page fault data for prefetch mapping.
func (s *Sandbox) MemoryPrefetchData(ctx context.Context) (block.PrefetchData, error) {
	prefetchData, err := s.Resources.memory.PrefetchData(ctx)
	if err != nil {
		return block.PrefetchData{}, fmt.Errorf("failed to get prefetch data: %w", err)
	}

	return prefetchData, nil
}

func pauseProcessMemory(
	ctx context.Context,
	buildID uuid.UUID,
	originalHeader *header.Header,
	diffMetadata *header.DiffMetadata,
	cacheDir string,
	fc *fc.Process,
) (d build.Diff, h *header.Header, e error) {
	ctx, span := tracer.Start(ctx, "process-memory")
	defer span.End()

	header, err := diffMetadata.ToDiffHeader(ctx, originalHeader, buildID)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create memfile header: %w", err)
	}

	memfileDiffPath := build.GenerateDiffCachePath(cacheDir, buildID.String(), build.Memfile)

	cache, err := fc.ExportMemory(
		ctx,
		diffMetadata.Dirty,
		memfileDiffPath,
		diffMetadata.BlockSize,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to export memory: %w", err)
	}

	diff, err := build.NewLocalDiffFromCache(
		build.GetDiffStoreKey(buildID.String(), build.Memfile),
		cache,
	)
	if err != nil {
		// Close the cache even if the diff creation fails.
		return nil, nil, fmt.Errorf("failed to create local diff from cache: %w", errors.Join(err, cache.Close()))
	}

	return diff, header, nil
}

func pauseProcessRootfs(
	ctx context.Context,
	buildId uuid.UUID,
	originalHeader *header.Header,
	diffCreator DiffCreator,
	cacheDir string,
) (d build.Diff, h *header.Header, e error) {
	ctx, span := tracer.Start(ctx, "process-rootfs")
	defer span.End()

	rootfsDiffFile, err := build.NewLocalDiffFile(cacheDir, buildId.String(), build.Rootfs)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create rootfs diff: %w", err)
	}

	rootfsDiffMetadata, err := diffCreator.process(ctx, rootfsDiffFile)
	if err != nil {
		err = errors.Join(err, rootfsDiffFile.Close())

		return nil, nil, fmt.Errorf("error creating diff: %w", err)
	}
	telemetry.ReportEvent(ctx, "exported rootfs")

	rootfsDiff, err := rootfsDiffFile.CloseToDiff(int64(originalHeader.Metadata.BlockSize))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to convert rootfs diff file to local diff: %w", err)
	}
	telemetry.ReportEvent(ctx, "converted rootfs diff file to local diff")

	rootfsHeader, err := rootfsDiffMetadata.ToDiffHeader(ctx, originalHeader, buildId)
	if err != nil {
		err = errors.Join(err, rootfsDiff.Close())

		return nil, nil, fmt.Errorf("failed to create rootfs header: %w", err)
	}

	return rootfsDiff, rootfsHeader, nil
}

func getNetworkSlot(
	ctx context.Context,
	networkPool *network.Pool,
	cleanup *Cleanup,
	networkConfig *orchestrator.SandboxNetworkConfig,
) *utils.Promise[*network.Slot] {
	return utils.NewPromise(func() (*network.Slot, error) {
		ctx, span := tracer.Start(ctx, "get network-slot")
		defer span.End()

		slot, err := networkPool.Get(ctx, networkConfig)
		if err != nil {
			return nil, fmt.Errorf("failed to get network slot: %w", err)
		}

		cleanup.Add(ctx, func(ctx context.Context) error {
			ctx, span := tracer.Start(ctx, "clean network-slot")
			defer span.End()

			// We can run this cleanup asynchronously, as it is not important for the sandbox lifecycle
			go func(ctx context.Context) {
				returnErr := networkPool.Return(ctx, slot)
				if returnErr != nil {
					logger.L().Error(ctx, "failed to return network slot", zap.Error(returnErr))
				}
			}(context.WithoutCancel(ctx))

			return nil
		})

		return slot, nil
	})
}

func serveMemory(
	ctx context.Context,
	cleanup *Cleanup,
	fcUffd *uffd.Uffd,
	sandboxID string,
) error {
	ctx, span := tracer.Start(ctx, "serve-memory")
	defer span.End()

	telemetry.ReportEvent(ctx, "created uffd")

	if err := fcUffd.Start(ctx, sandboxID); err != nil {
		return fmt.Errorf("failed to start uffd: %w", err)
	}

	telemetry.ReportEvent(ctx, "started uffd")

	cleanup.Add(ctx, func(ctx context.Context) error {
		_, span := tracer.Start(ctx, "uffd-stop")
		defer span.End()

		if err := fcUffd.Stop(); err != nil {
			return fmt.Errorf("failed to stop uffd: %w", err)
		}

		return nil
	})

	return nil
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
	envdInitRequestTimeoutMs := f.featureFlags.IntFlag(ctx, featureflags.EnvdInitTimeoutMilliseconds)

	return time.Duration(envdInitRequestTimeoutMs) * time.Millisecond
}
