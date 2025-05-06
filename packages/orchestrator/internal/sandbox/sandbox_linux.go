//go:build linux
// +build linux

package sandbox

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"golang.org/x/mod/semver"

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

type Sandbox struct {
	files   *storage.SandboxFiles
	cleanup *Cleanup

	process *fc.Process
	uffd    *uffd.Uffd
	rootfs  *rootfs.CowDevice

	Config    *orchestrator.SandboxConfig
	StartedAt time.Time
	EndAt     time.Time

	Slot network.Slot

	uffdExit chan error

	template template.Template

	healthcheckCtx *utils.LockableCancelableContext
	healthy        atomic.Bool

	ClickhouseStore chdb.Store

	//
	useLokiMetrics       string
	useClickhouseMetrics string
	StartID              string
}

func (s *Sandbox) LoggerMetadata() sbxlogger.SandboxMetadata {
	return sbxlogger.SandboxMetadata{
		SandboxID:  s.Config.SandboxId,
		TemplateID: s.Config.TemplateId,
		TeamID:     s.Config.TeamId,
	}
}

// Run cleanup functions for the already initialized resources if there is any error or after you are done with the started sandbox.
func StartSandbox(
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

	networkCtx, networkSpan := tracer.Start(childCtx, "get-network-slot")
	defer networkSpan.End()

	ips, err := networkPool.Get(networkCtx)
	if err != nil {
		return nil, cleanup, fmt.Errorf("failed to get network slot: %w", err)
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
	networkSpan.End()

	sandboxFiles := t.Files().NewSandboxFiles(config.SandboxId)

	cleanup.Add(func(ctx context.Context) error {
		filesErr := cleanupFiles(sandboxFiles)
		if filesErr != nil {
			return fmt.Errorf("failed to cleanup files: %w", filesErr)
		}

		return nil
	})

	_, overlaySpan := tracer.Start(childCtx, "create-rootfs-overlay")
	defer overlaySpan.End()

	readonlyRootfs, err := t.Rootfs()
	if err != nil {
		return nil, cleanup, fmt.Errorf("failed to get rootfs: %w", err)
	}

	rootfsOverlay, err := rootfs.NewCowDevice(
		tracer,
		readonlyRootfs,
		sandboxFiles.SandboxCacheRootfsPath(),
		devicePool,
	)
	if err != nil {
		return nil, cleanup, fmt.Errorf("failed to create overlay file: %w", err)
	}

	cleanup.Add(func(ctx context.Context) error {
		childCtx, span := tracer.Start(ctx, "rootfs-overlay-close")
		defer span.End()

		if rootfsOverlayErr := rootfsOverlay.Close(childCtx); rootfsOverlayErr != nil {
			return fmt.Errorf("failed to close overlay file: %w", rootfsOverlayErr)
		}

		return nil
	})

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
	overlaySpan.End()

	fcUffd, uffdErr := uffd.New(memfile, sandboxFiles.SandboxUffdSocketPath(), memfile.BlockSize(), clientID)
	if uffdErr != nil {
		return nil, cleanup, fmt.Errorf("failed to create uffd: %w", uffdErr)
	}

	uffdStartErr := fcUffd.Start(config.SandboxId)
	if uffdStartErr != nil {
		return nil, cleanup, fmt.Errorf("failed to start uffd: %w", uffdStartErr)
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

	uffdExit := make(chan error, 1)

	uffdStartCtx, cancelUffdStartCtx := context.WithCancelCause(childCtx)
	defer cancelUffdStartCtx(fmt.Errorf("uffd finished starting"))

	go func() {
		uffdWaitErr := <-fcUffd.Exit
		uffdExit <- uffdWaitErr

		cancelUffdStartCtx(fmt.Errorf("uffd process exited: %w", errors.Join(uffdWaitErr, context.Cause(uffdStartCtx))))
	}()

	// todo: check if kernel, firecracker, and envd versions exist
	snapfile, err := t.Snapfile()
	if err != nil {
		return nil, cleanup, fmt.Errorf("failed to get snapfile: %w", err)
	}

	fcHandle, fcErr := fc.NewProcess(
		uffdStartCtx,
		tracer,
		ips,
		sandboxFiles,
		&fc.MmdsMetadata{
			SandboxId:            config.SandboxId,
			TemplateId:           config.TemplateId,
			LogsCollectorAddress: os.Getenv("LOGS_COLLECTOR_PUBLIC_IP"),
			TraceId:              traceID,
			TeamId:               config.TeamId,
		},
		snapfile,
		rootfsOverlay,
		fcUffd.Ready,
		baseTemplateID,
	)
	if fcErr != nil {
		return nil, cleanup, fmt.Errorf("failed to create FC: %w", fcErr)
	}

	fcStartErr := fcHandle.Start(uffdStartCtx, tracer)
	if fcStartErr != nil {
		return nil, cleanup, fmt.Errorf("failed to start FC: %w", fcStartErr)
	}

	telemetry.ReportEvent(childCtx, "initialized FC")

	healthcheckCtx := utils.NewLockableCancelableContext(context.Background())

	sbx := &Sandbox{
		uffdExit:        uffdExit,
		files:           sandboxFiles,
		Slot:            ips,
		template:        t,
		process:         fcHandle,
		uffd:            fcUffd,
		Config:          config,
		StartedAt:       startedAt,
		EndAt:           endAt,
		rootfs:          rootfsOverlay,
		cleanup:         cleanup,
		healthcheckCtx:  healthcheckCtx,
		healthy:         atomic.Bool{}, // defaults to `false`
		ClickhouseStore: clickhouseStore,
		StartID:         uuid.New().String(),
	}
	// By default, the sandbox should be healthy, if the status change we report it.
	sbx.healthy.Store(true)

	cleanup.AddPriority(func(ctx context.Context) error {
		_, span := tracer.Start(ctx, "fc-uffd-stop")
		defer span.End()

		var errs []error

		fcStopErr := fcHandle.Stop()
		if fcStopErr != nil {
			errs = append(errs, fmt.Errorf("failed to stop FC: %w", fcStopErr))
		}

		uffdStopErr := fcUffd.Stop()
		if uffdStopErr != nil {
			errs = append(errs, fmt.Errorf("failed to stop uffd: %w", uffdStopErr))
		}

		healthcheckCtx.Lock()
		healthcheckCtx.Cancel()
		healthcheckCtx.Unlock()

		return errors.Join(errs...)
	})

	// Ensure the syncing takes at most envdTimeout seconds.
	syncCtx, syncCancel := context.WithTimeoutCause(childCtx, envdTimeout, fmt.Errorf("syncing took too long"))
	defer syncCancel()

	// Sync envds.
	if semver.Compare(fmt.Sprintf("v%s", config.EnvdVersion), "v0.1.1") >= 0 {
		initErr := sbx.initEnvd(syncCtx, tracer, config.EnvVars, config.EnvdAccessToken)
		if initErr != nil {
			return nil, cleanup, fmt.Errorf("failed to init new envd: %w", initErr)
		} else {
			telemetry.ReportEvent(childCtx, fmt.Sprintf("[sandbox %s]: initialized new envd", config.SandboxId))
		}
	} else {
		syncErr := sbx.syncOldEnvd(syncCtx)
		if syncErr != nil {
			telemetry.ReportError(childCtx, fmt.Errorf("failed to sync old envd: %w", syncErr))
		} else {
			telemetry.ReportEvent(childCtx, fmt.Sprintf("[sandbox %s]: synced old envd", config.SandboxId))
		}
	}

	sbx.StartedAt = time.Now()

	go sbx.logHeathAndUsage(healthcheckCtx)

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

func (s *Sandbox) Stop(ctx context.Context) error {
	err := s.cleanup.Run(ctx)
	if err != nil {
		return fmt.Errorf("failed to stop sandbox: %w", err)
	}

	return nil
}

func (s *Sandbox) Pause(
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
	s.healthcheckCtx.Lock()
	s.healthcheckCtx.Cancel()
	s.healthcheckCtx.Unlock()

	if err := s.process.Pause(ctx, tracer); err != nil {
		return nil, fmt.Errorf("failed to pause VM: %w", err)
	}

	if err := s.uffd.Disable(); err != nil {
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
		originalMemfile,
		&MemoryDiffCreator{
			tracer:     tracer,
			memfile:    memfile,
			dirtyPages: s.uffd.Dirty(),
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
		originalRootfs,
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
	originalMemfile *template.Storage,
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
		originalMemfile.Header().Mapping,
		memfileMapping,
	)
	// TODO: We can run normalization only when empty mappings are not empty for this snapshot
	memfileMappings = header.NormalizeMappings(memfileMappings)
	telemetry.ReportEvent(ctx, "merged memfile mappings")

	memfileDiff, err := memfileDiffFile.CloseToDiff(int64(originalMemfile.Header().Metadata.BlockSize))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to convert memfile diff file to local diff: %w", err)
	}

	telemetry.ReportEvent(ctx, "converted memfile diff file to local diff")

	memfileMetadata := originalMemfile.Header().Metadata.NextGeneration(buildId)

	telemetry.SetAttributes(ctx,
		attribute.Int64("snapshot.memfile.header.mappings.length", int64(len(memfileMappings))),
		attribute.Int64("snapshot.memfile.diff.size", int64(m.Dirty.Count()*uint(originalMemfile.Header().Metadata.BlockSize))),
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
	originalRootfs *template.Storage,
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
		originalRootfs.Header().Mapping,
		rootfsMapping,
	)
	// TODO: We can run normalization only when empty mappings are not empty for this snapshot
	rootfsMappings = header.NormalizeMappings(rootfsMappings)
	telemetry.ReportEvent(ctx, "merged rootfs mappings")

	rootfsDiff, err := rootfsDiffFile.CloseToDiff(int64(originalRootfs.Header().Metadata.BlockSize))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to convert rootfs diff file to local diff: %w", err)
	}
	telemetry.ReportEvent(ctx, "converted rootfs diff file to local diff")

	rootfsMetadata := originalRootfs.Header().Metadata.NextGeneration(buildId)

	telemetry.SetAttributes(ctx,
		attribute.Int64("snapshot.rootfs.header.mappings.length", int64(len(rootfsMappings))),
		attribute.Int64("snapshot.rootfs.diff.size", int64(rootfsDiffMetadata.Dirty.Count()*uint(originalRootfs.Header().Metadata.BlockSize))),
		attribute.Int64("snapshot.rootfs.mapped_size", int64(rootfsMetadata.Size)),
		attribute.Int64("snapshot.rootfs.block_size", int64(rootfsMetadata.BlockSize)),
	)

	return rootfsDiff, header.NewHeader(rootfsMetadata, rootfsMappings), nil
}
