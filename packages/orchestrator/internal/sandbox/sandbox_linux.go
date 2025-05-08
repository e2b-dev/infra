//go:build linux
// +build linux

package sandbox

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"golang.org/x/mod/semver"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/build"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/fc"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/nbd"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/network"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/rootfs"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/template"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd"
	"github.com/e2b-dev/infra/packages/shared/pkg/chdb"
	"github.com/e2b-dev/infra/packages/shared/pkg/env"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

var envdTimeout = utils.Must(time.ParseDuration(env.GetEnv("ENVD_TIMEOUT", "10s")))

var httpClient = http.Client{
	Timeout: 10 * time.Second,
}

type Resources struct {
	Slot     network.Slot
	rootfs   *rootfs.CowDevice
	memory   uffd.MemoryBackend
	uffdExit chan error
}

type Metadata struct {
	Config    *orchestrator.SandboxConfig
	StartedAt time.Time
	EndAt     time.Time
	StartID   string
}

type Sandbox struct {
	*Resources
	*Metadata

	files   *storage.SandboxFiles
	cleanup *Cleanup

	process *fc.Process

	template template.Template

	ClickhouseStore chdb.Store

	Checks *Checks
}

func (m *Metadata) LoggerMetadata() sbxlogger.SandboxMetadata {
	return sbxlogger.SandboxMetadata{
		SandboxID:  m.Config.SandboxId,
		TemplateID: m.Config.TemplateId,
		TeamID:     m.Config.TeamId,
	}
}

func CreateSandbox(
	ctx context.Context,
	tracer trace.Tracer,
	networkPool *network.Pool,
	devicePool *nbd.DevicePool,
	config *orchestrator.SandboxConfig,
	rootfs block.ReadonlyDevice,
	sandboxTimeout time.Duration,
) (*Sandbox, *Cleanup, error) {
	childCtx, childSpan := tracer.Start(ctx, "new-sandbox")
	defer childSpan.End()

	cleanup := NewCleanup()

	t, err := storage.NewTemplateFiles(
		config.TemplateId,
		config.BuildId,
		config.KernelVersion,
		config.FirecrackerVersion,
	).NewTemplateCacheFiles()
	if err != nil {
		return nil, cleanup, fmt.Errorf("failed to create template cache files: %w", err)
	}

	ips, err := getNetworkSlot(childCtx, tracer, networkPool, cleanup)
	if err != nil {
		return nil, cleanup, fmt.Errorf("failed to get network slot: %w", err)
	}

	sandboxFiles := t.NewSandboxFiles(config.SandboxId)
	cleanup.Add(func(ctx context.Context) error {
		filesErr := cleanupFiles(sandboxFiles)
		if filesErr != nil {
			return fmt.Errorf("failed to cleanup files: %w", filesErr)
		}

		return nil
	})

	rootfsOverlay, err := createRootfsOverlay(
		childCtx,
		tracer,
		devicePool,
		cleanup,
		rootfs,
		sandboxFiles.SandboxCacheRootfsPath(),
	)
	if err != nil {
		return nil, cleanup, fmt.Errorf("failed to create rootfs overlay: %w", err)
	}
	rootfsOverlay.MarkAllBlocksAsDirty()

	go func() {
		runErr := rootfsOverlay.Start(childCtx)
		if runErr != nil {
			zap.L().Error("rootfs overlay error", zap.Error(runErr))
		}
	}()

	resources := &Resources{
		Slot:     ips,
		rootfs:   rootfsOverlay,
		memory:   &uffd.NoopMemory{},
		uffdExit: make(chan error, 1),
	}

	/// ==== END of resources initialization ====

	fcHandle, err := fc.NewProcess(
		childCtx,
		tracer,
		ips,
		sandboxFiles,
		rootfsOverlay,
		config.BaseTemplateId,
		config.BuildId,
	)
	if err != nil {
		return nil, cleanup, fmt.Errorf("failed to init FC: %w", err)
	}

	telemetry.ReportEvent(childCtx, "created fc client")

	err = fcHandle.Create(
		childCtx,
		tracer,
		config.TemplateId,
		config.TeamId,
		config.Vcpu,
		config.RamMb,
		config.HugePages,
	)
	if err != nil {
		return nil, cleanup, fmt.Errorf("failed to create FC: %w", err)
	}
	telemetry.ReportEvent(childCtx, "created fc process")

	metadata := &Metadata{
		Config: config,

		StartID:   uuid.New().String(),
		StartedAt: time.Now(),
		EndAt:     time.Now().Add(sandboxTimeout),
	}

	sbx := &Sandbox{
		Resources: resources,
		Metadata:  metadata,

		files:   sandboxFiles,
		process: fcHandle,

		cleanup: cleanup,

		ClickhouseStore: nil,
	}

	sbx.Checks = NewChecks(sbx, "", "")

	cleanup.AddPriority(func(ctx context.Context) error {
		return sbx.Close(ctx, tracer)
	})

	err = sbx.waitForStart(
		ctx,
		tracer,
	)
	if err != nil {
		return nil, cleanup, fmt.Errorf("failed to wait for sandbox start: %w", err)
	}

	/* TODO
	if env.StartCmd != "" {
		postProcessor.WriteMsg("Waiting for start command to run...")
		// HACK: This is a temporary fix for a customer that needs a bigger time to start the command.
		// TODO: Remove this after we can add customizable wait time for building templates.
		if env.TemplateId == "zegbt9dl3l2ixqem82mm" || env.TemplateId == "ot5bidkk3j2so2j02uuz" || env.TemplateId == "0zeou1s7agaytqitvmzc" {
			time.Sleep(120 * time.Second)
		} else {
			time.Sleep(waitTimeForStartCmd)
		}
		postProcessor.WriteMsg("Start command is running")
		telemetry.ReportEvent(childCtx, "waited for start command", attribute.Float64("seconds", float64(waitTimeForStartCmd/time.Second)))
	}
	*/

	// Set the sandbox as started now
	sbx.Metadata.StartedAt = time.Now()

	return sbx, cleanup, nil
}

// ResumeSandbox resumes the sandbox from already saved template or snapshot.
// IMPORTANT: You have to run cleanup functions for the already initialized resources even if there is any error,
// or after you are done with the started sandbox.
func ResumeSandbox(
	ctx context.Context,
	tracer trace.Tracer,
	networkPool *network.Pool,
	templateCache *template.Cache,
	config *orchestrator.SandboxConfig,
	traceID string,
	startedAt time.Time,
	endAt time.Time,
	baseTemplateID string,
	clientID string,
	devicePool *nbd.DevicePool,
	clickhouseStore chdb.Store,
	useLokiMetrics string,
	useClickhouseMetrics string,
) (*Sandbox, *Cleanup, error) {
	childCtx, childSpan := tracer.Start(ctx, "new-sandbox")
	defer childSpan.End()

	cleanup := NewCleanup()

	t, err := templateCache.GetTemplate(
		config.TemplateId,
		config.BuildId,
		config.KernelVersion,
		config.FirecrackerVersion,
	)
	if err != nil {
		return nil, cleanup, fmt.Errorf("failed to get template snapshot data: %w", err)
	}

	ips, err := getNetworkSlot(childCtx, tracer, networkPool, cleanup)
	if err != nil {
		return nil, cleanup, fmt.Errorf("failed to get network slot: %w", err)
	}

	sandboxFiles := t.Files().NewSandboxFiles(config.SandboxId)
	cleanup.Add(func(ctx context.Context) error {
		filesErr := cleanupFiles(sandboxFiles)
		if filesErr != nil {
			return fmt.Errorf("failed to cleanup files: %w", filesErr)
		}

		return nil
	})

	readonlyRootfs, err := t.Rootfs()
	if err != nil {
		return nil, cleanup, fmt.Errorf("failed to get rootfs: %w", err)
	}

	rootfsOverlay, err := createRootfsOverlay(
		childCtx,
		tracer,
		devicePool,
		cleanup,
		readonlyRootfs,
		sandboxFiles.SandboxCacheRootfsPath(),
	)

	go func() {
		runErr := rootfsOverlay.Start(childCtx)
		if runErr != nil {
			zap.L().Error("rootfs overlay error", zap.Error(runErr))
		}
	}()

	memfile, err := t.Memfile()
	if err != nil {
		return nil, cleanup, fmt.Errorf("failed to get memfile: %w", err)
	}

	fcUffdPath := sandboxFiles.SandboxUffdSocketPath()

	fcUffd, err := serveMemory(
		childCtx,
		tracer,
		cleanup,
		memfile,
		fcUffdPath,
		config.SandboxId,
		clientID,
	)
	if err != nil {
		return nil, cleanup, fmt.Errorf("failed to serve memory: %w", err)
	}

	uffdStartCtx, cancelUffdStartCtx := context.WithCancelCause(ctx)
	defer cancelUffdStartCtx(fmt.Errorf("uffd finished starting"))

	uffdExit := make(chan error, 1)
	go func() {
		uffdWaitErr := <-fcUffd.Exit()
		uffdExit <- uffdWaitErr

		cancelUffdStartCtx(fmt.Errorf("uffd process exited: %w", errors.Join(uffdWaitErr, context.Cause(uffdStartCtx))))
	}()

	resources := &Resources{
		Slot:     ips,
		rootfs:   rootfsOverlay,
		memory:   fcUffd,
		uffdExit: uffdExit,
	}

	/// ==== END of resources initialization ====

	fcHandle, fcErr := fc.NewProcess(
		uffdStartCtx,
		tracer,
		ips,
		sandboxFiles,
		rootfsOverlay,
		baseTemplateID,
		readonlyRootfs.Header().Metadata.BaseBuildId.String(),
	)
	if fcErr != nil {
		return nil, cleanup, fmt.Errorf("failed to create FC: %w", fcErr)
	}

	// todo: check if kernel, firecracker, and envd versions exist
	snapfile, err := t.Snapfile()
	if err != nil {
		return nil, cleanup, fmt.Errorf("failed to get snapfile: %w", err)
	}
	fcStartErr := fcHandle.Resume(
		uffdStartCtx,
		tracer,
		&fc.MmdsMetadata{
			SandboxId:            config.SandboxId,
			TemplateId:           config.TemplateId,
			LogsCollectorAddress: os.Getenv("LOGS_COLLECTOR_PUBLIC_IP"),
			TraceId:              traceID,
			TeamId:               config.TeamId,
		},
		fcUffdPath,
		snapfile,
		fcUffd.Ready(),
	)
	if fcStartErr != nil {
		return nil, cleanup, fmt.Errorf("failed to start FC: %w", fcStartErr)
	}

	telemetry.ReportEvent(childCtx, "initialized FC")

	metadata := &Metadata{
		Config: config,

		StartID:   uuid.New().String(),
		StartedAt: startedAt,
		EndAt:     endAt,
	}

	sbx := &Sandbox{
		Resources: resources,
		Metadata:  metadata,

		files:   sandboxFiles,
		process: fcHandle,

		cleanup: cleanup,

		ClickhouseStore: clickhouseStore,
	}

	// TODO: Part of the sandbox as we need to stop Checks before pausing the sandbox (why?)
	sbx.Checks = NewChecks(sbx, useLokiMetrics, useClickhouseMetrics)

	cleanup.AddPriority(func(ctx context.Context) error {
		return sbx.Close(ctx, tracer)
	})

	err = sbx.waitForStart(
		ctx,
		tracer,
	)
	if err != nil {
		return nil, cleanup, fmt.Errorf("failed to wait for sandbox start: %w", err)
	}

	// Set the sandbox as started now
	sbx.Metadata.StartedAt = time.Now()

	go sbx.Checks.Start()

	return sbx, cleanup, nil
}

func (s *Sandbox) Wait(ctx context.Context) error {
	select {
	case fcErr := <-s.process.Exit:
		stopErr := s.Stop(ctx)
		uffdErr := <-s.uffdExit

		return errors.Join(fcErr, stopErr, uffdErr)
	case uffdErr := <-s.uffdExit:
		stopErr := s.Stop(ctx)
		fcErr := <-s.process.Exit

		return errors.Join(uffdErr, stopErr, fcErr)
	}
}

// Stop starts the cleanup process for the sandbox.
func (s *Sandbox) Stop(ctx context.Context) error {
	err := s.cleanup.Run(ctx)
	if err != nil {
		return fmt.Errorf("failed to stop sandbox: %w", err)
	}

	return nil
}

// Close cleans up the sandbox and stops all resources.
func (s *Sandbox) Close(ctx context.Context, tracer trace.Tracer) error {
	_, span := tracer.Start(ctx, "fc-uffd-stop")
	defer span.End()

	var errs []error

	fcStopErr := s.process.Stop()
	if fcStopErr != nil {
		errs = append(errs, fmt.Errorf("failed to stop FC: %w", fcStopErr))
	}

	uffdStopErr := s.Resources.memory.Stop()
	if uffdStopErr != nil {
		errs = append(errs, fmt.Errorf("failed to stop uffd: %w", uffdStopErr))
	}

	s.Checks.Stop()

	return errors.Join(errs...)
}

func (s *Sandbox) Pause(
	ctx context.Context,
	tracer trace.Tracer,
	snapshotTemplateFiles *storage.TemplateCacheFiles,
) (*Snapshot, error) {
	return s.PauseWithLockRelease(
		ctx,
		tracer,
		snapshotTemplateFiles,
		func() {},
	)
}

func (s *Sandbox) PauseWithLockRelease(
	ctx context.Context,
	tracer trace.Tracer,
	snapshotTemplateFiles *storage.TemplateCacheFiles,
	releaseLock func(),
) (*Snapshot, error) {
	ctx, childSpan := tracer.Start(ctx, "sandbox-snapshot")
	defer childSpan.End()

	buildID, err := uuid.Parse(snapshotTemplateFiles.BuildId)
	if err != nil {
		return nil, fmt.Errorf("failed to parse build id: %w", err)
	}

	// Stop the health check before pausing the VM
	s.Checks.Stop()

	if err := s.process.Pause(ctx, tracer); err != nil {
		return nil, fmt.Errorf("failed to pause VM: %w", err)
	}

	if err := s.memory.Disable(); err != nil {
		return nil, fmt.Errorf("failed to disable uffd: %w", err)
	}

	// Snapfile is not closed as it's returned and reused for resume
	snapfile := template.NewLocalFileLink(snapshotTemplateFiles.CacheSnapfilePath())
	memfile := template.NewLocalFileLink(snapshotTemplateFiles.CacheMemfileFullSnapshotPath())
	defer memfile.Close()

	err = s.process.CreateSnapshot(
		ctx,
		tracer,
		snapfile.Path(),
		memfile.Path(),
	)
	if err != nil {
		return nil, fmt.Errorf("error creating snapshot: %w", err)
	}

	// TODO: When is the right time to release the lock?
	releaseLock()

	// Gather data for postprocessing
	originalMemfile, err := s.template.Memfile()
	if err != nil {
		return nil, fmt.Errorf("failed to get original memfile: %w", err)
	}
	originalRootfs, err := s.template.Rootfs()
	if err != nil {
		return nil, fmt.Errorf("failed to get original rootfs: %w", err)
	}

	// Start POSTPROCESSING
	memfileDiff, memfileDiffHeader, err := pauseProcessMemory(
		ctx,
		tracer,
		buildID,
		originalMemfile.Header(),
		&MemoryDiffCreator{
			tracer:     tracer,
			memfile:    memfile,
			dirtyPages: s.memory.Dirty(),
			blockSize:  originalMemfile.BlockSize(),
		},
	)
	if err != nil {
		return nil, fmt.Errorf("error while post processing: %w", err)
	}

	rootfsDiff, rootfsDiffHeader, err := pauseProcessRootfs(
		ctx,
		tracer,
		buildID,
		originalRootfs.Header(),
		&RootfsDiffCreator{
			rootfs:   s.rootfs,
			stopHook: s.Stop,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("error while post processing: %w", err)
	}

	return &Snapshot{
		Snapfile:          snapfile,
		MemfileDiff:       memfileDiff,
		MemfileDiffHeader: memfileDiffHeader,
		RootfsDiff:        rootfsDiff,
		RootfsDiffHeader:  rootfsDiffHeader,
	}, nil
}

type Snapshot struct {
	MemfileDiff       build.Diff
	MemfileDiffHeader *header.Header
	RootfsDiff        build.Diff
	RootfsDiffHeader  *header.Header
	Snapfile          *template.LocalFileLink
}

func pauseProcessMemory(
	ctx context.Context,
	tracer trace.Tracer,
	buildId uuid.UUID,
	originalHeader *header.Header,
	diffCreator DiffCreator,
) (build.Diff, *header.Header, error) {
	ctx, childSpan := tracer.Start(ctx, "process-memory")
	defer childSpan.End()

	memfileDiffFile, err := build.NewLocalDiffFile(
		build.DefaultCachePath,
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

	// TODO: Do we need to release here or can we earlier?
	// Release the lock to allow other operations to proceed

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

	return memfileDiff, header.NewHeader(memfileMetadata, memfileMappings), nil
}

func pauseProcessRootfs(
	ctx context.Context,
	tracer trace.Tracer,
	buildId uuid.UUID,
	originalHeader *header.Header,
	diffCreator DiffCreator,
) (build.Diff, *header.Header, error) {
	ctx, childSpan := tracer.Start(ctx, "process-rootfs")
	defer childSpan.End()

	rootfsDiffFile, err := build.NewLocalDiffFile(build.DefaultCachePath, buildId.String(), build.Rootfs)
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

	return rootfsDiff, header.NewHeader(rootfsMetadata, rootfsMappings), nil
}

func getNetworkSlot(
	ctx context.Context,
	tracer trace.Tracer,
	networkPool *network.Pool,
	cleanup *Cleanup,
) (network.Slot, error) {
	networkCtx, networkSpan := tracer.Start(ctx, "get-network-slot")
	defer networkSpan.End()

	ips, err := networkPool.Get(networkCtx)
	if err != nil {
		return network.Slot{}, fmt.Errorf("failed to get network slot: %w", err)
	}

	cleanup.Add(func(ctx context.Context) error {
		_, span := tracer.Start(ctx, "network-slot-clean")
		defer span.End()

		returnErr := networkPool.Return(ips)
		if returnErr != nil {
			return fmt.Errorf("failed to return network slot: %w", returnErr)
		}

		return nil
	})

	return ips, nil
}

func createRootfsOverlay(
	ctx context.Context,
	tracer trace.Tracer,
	devicePool *nbd.DevicePool,
	cleanup *Cleanup,
	readonlyRootfs block.ReadonlyDevice,
	targetCachePath string,
) (*rootfs.CowDevice, error) {
	_, overlaySpan := tracer.Start(ctx, "create-rootfs-overlay")
	defer overlaySpan.End()

	rootfsOverlay, err := rootfs.NewCowDevice(
		tracer,
		readonlyRootfs,
		targetCachePath,
		devicePool,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create overlay file: %w", err)
	}

	cleanup.Add(func(ctx context.Context) error {
		childCtx, span := tracer.Start(ctx, "rootfs-overlay-close")
		defer span.End()

		if rootfsOverlayErr := rootfsOverlay.Close(childCtx); rootfsOverlayErr != nil {
			return fmt.Errorf("failed to close overlay file: %w", rootfsOverlayErr)
		}

		return nil
	})

	return rootfsOverlay, nil
}

func serveMemory(
	ctx context.Context,
	tracer trace.Tracer,
	cleanup *Cleanup,
	memfile block.ReadonlyDevice,
	socketPath string,
	sandboxID string,
	clientID string,
) (uffd.MemoryBackend, error) {
	fcUffd, uffdErr := uffd.New(memfile, socketPath, memfile.BlockSize(), clientID)
	if uffdErr != nil {
		return nil, fmt.Errorf("failed to create uffd: %w", uffdErr)
	}

	uffdStartErr := fcUffd.Start(sandboxID)
	if uffdStartErr != nil {
		return nil, fmt.Errorf("failed to start uffd: %w", uffdStartErr)
	}

	cleanup.Add(func(ctx context.Context) error {
		_, span := tracer.Start(ctx, "uffd-stop")
		defer span.End()

		stopErr := fcUffd.Stop()
		if stopErr != nil {
			return fmt.Errorf("failed to stop uffd: %w", stopErr)
		}

		return nil
	})

	return fcUffd, nil
}

func (s *Sandbox) waitForStart(
	ctx context.Context,
	tracer trace.Tracer,
) error {
	// Ensure the syncing takes at most envdTimeout seconds.
	syncCtx, syncCancel := context.WithTimeoutCause(ctx, envdTimeout, fmt.Errorf("syncing took too long"))
	defer syncCancel()

	if semver.Compare(fmt.Sprintf("v%s", s.Metadata.Config.EnvdVersion), "v0.1.1") >= 0 {
		initErr := s.initEnvd(syncCtx, tracer, s.Metadata.Config.EnvVars, s.Metadata.Config.EnvdAccessToken)
		if initErr != nil {
			return fmt.Errorf("failed to init new envd: %w", initErr)
		} else {
			telemetry.ReportEvent(syncCtx, fmt.Sprintf("[sandbox %s]: initialized new envd", s.Metadata.Config.SandboxId))
		}
	} else {
		syncErr := s.syncOldEnvd(syncCtx)
		if syncErr != nil {
			telemetry.ReportError(syncCtx, fmt.Errorf("failed to sync old envd: %w", syncErr))
			return fmt.Errorf("failed to sync old envd: %w", syncErr)
		} else {
			telemetry.ReportEvent(syncCtx, fmt.Sprintf("[sandbox %s]: synced old envd", s.Metadata.Config.SandboxId))
		}
	}

	return nil
}
