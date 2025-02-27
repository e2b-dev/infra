package sandbox

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"syscall"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"golang.org/x/mod/semver"
	"golang.org/x/sys/unix"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/dns"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/build"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/fc"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/network"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/rootfs"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/stats"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/template"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

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

	Slot   network.Slot
	Logger *sbxlogger.SandboxLogger
	stats  *stats.Handle

	uffdExit chan error

	template template.Template

	healthcheckCtx *utils.LockableCancelableContext
}

// Run cleanup functions for the already initialized resources if there is any error or after you are done with the started sandbox.
func NewSandbox(
	ctx context.Context,
	tracer trace.Tracer,
	dns *dns.DNS,
	networkPool *network.Pool,
	templateCache *template.Cache,
	config *orchestrator.SandboxConfig,
	traceID string,
	startedAt time.Time,
	endAt time.Time,
	sbxLogger *sbxlogger.SandboxLogger,
	isSnapshot bool,
	baseTemplateID string,
) (*Sandbox, *Cleanup, error) {
	childCtx, childSpan := tracer.Start(ctx, "new-sandbox")
	defer childSpan.End()

	cleanup := NewCleanup()

	t, err := templateCache.GetTemplate(
		config.TemplateId,
		config.BuildId,
		config.KernelVersion,
		config.FirecrackerVersion,
		config.HugePages,
		isSnapshot,
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

	cleanup.Add(func() error {
		returnErr := networkPool.Return(ips)
		if returnErr != nil {
			return fmt.Errorf("failed to return network slot: %w", returnErr)
		}

		return nil
	})
	networkSpan.End()

	sandboxFiles := t.Files().NewSandboxFiles(config.SandboxId)

	cleanup.Add(func() error {
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
		readonlyRootfs,
		sandboxFiles.SandboxCacheRootfsPath(),
		sandboxFiles.RootfsBlockSize(),
	)
	if err != nil {
		return nil, cleanup, fmt.Errorf("failed to create overlay file: %w", err)
	}

	cleanup.Add(func() error {
		rootfsOverlay.Close()

		return nil
	})

	go func() {
		runErr := rootfsOverlay.Start(childCtx)
		if runErr != nil {
			sbxLogger.Error("rootfs overlay error", zap.Error(runErr))
		}
	}()

	memfile, err := t.Memfile()
	if err != nil {
		return nil, cleanup, fmt.Errorf("failed to get memfile: %w", err)
	}
	overlaySpan.End()

	fcUffd, uffdErr := uffd.New(memfile, sandboxFiles.SandboxUffdSocketPath(), sandboxFiles.MemfilePageSize())
	if uffdErr != nil {
		return nil, cleanup, fmt.Errorf("failed to create uffd: %w", uffdErr)
	}

	uffdStartErr := fcUffd.Start(config.SandboxId)
	if uffdStartErr != nil {
		return nil, cleanup, fmt.Errorf("failed to start uffd: %w", uffdStartErr)
	}

	cleanup.Add(func() error {
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

	fcStartErr := fcHandle.Start(uffdStartCtx, tracer, sbxLogger)
	if fcStartErr != nil {
		return nil, cleanup, fmt.Errorf("failed to start FC: %w", fcStartErr)
	}

	telemetry.ReportEvent(childCtx, "initialized FC")

	healthcheckCtx := utils.NewLockableCancelableContext(context.Background())

	sbx := &Sandbox{
		uffdExit:       uffdExit,
		files:          sandboxFiles,
		Slot:           ips,
		template:       t,
		process:        fcHandle,
		uffd:           fcUffd,
		Config:         config,
		StartedAt:      startedAt,
		EndAt:          endAt,
		rootfs:         rootfsOverlay,
		Logger:         sbxLogger,
		cleanup:        cleanup,
		healthcheckCtx: healthcheckCtx,
	}

	cleanup.AddPriority(func() error {
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

	// Ensure the syncing takes at most 10 seconds.
	syncCtx, syncCancel := context.WithTimeout(childCtx, 10*time.Second)
	defer syncCancel()

	// Sync envds.
	if semver.Compare(fmt.Sprintf("v%s", config.EnvdVersion), "v0.1.1") >= 0 {
		initErr := sbx.initEnvd(syncCtx, tracer, config.EnvVars)
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

	dns.Add(config.SandboxId, ips.HostIP())

	telemetry.ReportEvent(childCtx, "added DNS record", attribute.String("ip", ips.HostIP()), attribute.String("hostname", config.SandboxId))

	cleanup.Add(func() error {
		dns.Remove(config.SandboxId, ips.HostIP())

		return nil
	})

	go sbx.logHeathAndUsage(healthcheckCtx)

	return sbx, cleanup, nil
}

func (s *Sandbox) Wait() error {
	select {
	case fcErr := <-s.process.Exit:
		stopErr := s.Stop()
		uffdErr := <-s.uffdExit

		return errors.Join(fcErr, stopErr, uffdErr)
	case uffdErr := <-s.uffdExit:
		stopErr := s.Stop()
		fcErr := <-s.process.Exit

		return errors.Join(uffdErr, stopErr, fcErr)
	}
}

func (s *Sandbox) Stop() error {
	err := s.cleanup.Run()
	if err != nil {
		return fmt.Errorf("failed to stop sandbox: %w", err)
	}

	return nil
}

func (s *Sandbox) Snapshot(
	ctx context.Context,
	tracer trace.Tracer,
	snapshotTemplateFiles *storage.TemplateCacheFiles,
	releaseLock func(),
) (*Snapshot, error) {
	ctx, childSpan := tracer.Start(ctx, "sandbox-snapshot")
	defer childSpan.End()

	buildId, err := uuid.Parse(snapshotTemplateFiles.BuildId)
	if err != nil {
		return nil, fmt.Errorf("failed to parse build id: %w", err)
	}

	// MEMFILE & SNAPFILE
	originalMemfile, err := s.template.Memfile()
	if err != nil {
		return nil, fmt.Errorf("failed to get original memfile: %w", err)
	}

	memfileMetadata := &header.Metadata{
		Version:     1,
		Generation:  originalMemfile.Header().Metadata.Generation + 1,
		BlockSize:   originalMemfile.Header().Metadata.BlockSize,
		Size:        originalMemfile.Header().Metadata.Size,
		BuildId:     buildId,
		BaseBuildId: originalMemfile.Header().Metadata.BaseBuildId,
	}

	s.healthcheckCtx.Lock()
	s.healthcheckCtx.Cancel()
	s.healthcheckCtx.Unlock()

	err = s.process.Pause(ctx, tracer)
	if err != nil {
		return nil, fmt.Errorf("error pausing vm: %w", err)
	}

	err = s.uffd.Disable()
	if err != nil {
		return nil, fmt.Errorf("failed to disable uffd: %w", err)
	}

	defer os.RemoveAll(snapshotTemplateFiles.CacheMemfileFullSnapshotPath())

	err = s.process.CreateSnapshot(
		ctx,
		tracer,
		snapshotTemplateFiles.CacheSnapfilePath(),
		snapshotTemplateFiles.CacheMemfileFullSnapshotPath(),
	)
	if err != nil {
		return nil, fmt.Errorf("error creating snapshot: %w", err)
	}

	memfileDirtyPages := s.uffd.Dirty()

	sourceFile, err := os.Open(snapshotTemplateFiles.CacheMemfileFullSnapshotPath())
	if err != nil {
		return nil, fmt.Errorf("failed to open memfile: %w", err)
	}

	memfileDiffFile, err := build.NewLocalDiffFile(
		buildId.String(),
		build.Memfile,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create memfile diff file: %w", err)
	}

	err = header.CreateDiff(sourceFile, s.files.MemfilePageSize(), memfileDirtyPages, memfileDiffFile)
	if err != nil {
		return nil, fmt.Errorf("failed to create memfile diff: %w", err)
	}

	telemetry.ReportEvent(ctx, "created memfile diff")

	os.RemoveAll(snapshotTemplateFiles.CacheMemfileFullSnapshotPath())

	releaseLock()

	memfileMapping := header.CreateMapping(
		memfileMetadata,
		&buildId,
		memfileDirtyPages,
	)

	telemetry.ReportEvent(ctx, "created memfile mapping")

	memfileMappings := header.MergeMappings(
		originalMemfile.Header().Mapping,
		memfileMapping,
	)

	telemetry.ReportEvent(ctx, "merged memfile mappings")

	snapfile, err := template.NewLocalFile(snapshotTemplateFiles.CacheSnapfilePath())
	if err != nil {
		return nil, fmt.Errorf("failed to create local snapfile: %w", err)
	}

	// ROOTFS
	originalRootfs, err := s.template.Rootfs()
	if err != nil {
		return nil, fmt.Errorf("failed to get original rootfs: %w", err)
	}

	rootfsMetadata := &header.Metadata{
		Version:     1,
		Generation:  originalRootfs.Header().Metadata.Generation + 1,
		BlockSize:   originalRootfs.Header().Metadata.BlockSize,
		Size:        originalRootfs.Header().Metadata.Size,
		BuildId:     buildId,
		BaseBuildId: originalRootfs.Header().Metadata.BaseBuildId,
	}

	nbdPath, err := s.rootfs.Path()
	if err != nil {
		return nil, fmt.Errorf("failed to get rootfs path: %w", err)
	}

	// Flush the data to the operating system's buffer
	file, err := os.Open(nbdPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open rootfs path: %w", err)
	}

	if err := unix.IoctlSetInt(int(file.Fd()), unix.BLKFLSBUF, 0); err != nil {
		return nil, fmt.Errorf("ioctl BLKFLSBUF failed: %w", err)
	}

	err = syscall.Fsync(int(file.Fd()))
	if err != nil {
		return nil, fmt.Errorf("failed to fsync rootfs path: %w", err)
	}

	err = file.Sync()
	if err != nil {
		return nil, fmt.Errorf("failed to sync rootfs path: %w", err)
	}

	telemetry.ReportEvent(ctx, "synced rootfs")

	rootfsDiffFile, err := build.NewLocalDiffFile(buildId.String(), build.Rootfs)
	if err != nil {
		return nil, fmt.Errorf("failed to create rootfs diff: %w", err)
	}

	rootfsDirtyBlocks, err := s.rootfs.Export(ctx, rootfsDiffFile, s.Stop)
	if err != nil {
		return nil, fmt.Errorf("failed to export rootfs: %w", err)
	}

	telemetry.ReportEvent(ctx, "exported rootfs")

	rootfsMapping := header.CreateMapping(
		rootfsMetadata,
		&buildId,
		rootfsDirtyBlocks,
	)

	telemetry.ReportEvent(ctx, "created rootfs mapping")

	rootfsMappings := header.MergeMappings(
		originalRootfs.Header().Mapping,
		rootfsMapping,
	)

	telemetry.ReportEvent(ctx, "merged rootfs mappings")

	rootfsDiff, err := rootfsDiffFile.ToDiff(int64(originalRootfs.Header().Metadata.BlockSize))
	if err != nil {
		return nil, fmt.Errorf("failed to convert rootfs diff file to local diff: %w", err)
	}

	telemetry.ReportEvent(ctx, "converted rootfs diff file to local diff")

	memfileDiff, err := memfileDiffFile.ToDiff(int64(originalMemfile.Header().Metadata.BlockSize))
	if err != nil {
		return nil, fmt.Errorf("failed to convert memfile diff file to local diff: %w", err)
	}

	telemetry.ReportEvent(ctx, "converted memfile diff file to local diff")

	telemetry.SetAttributes(ctx,
		attribute.Int64("snapshot.memfile.header.mappings.length", int64(len(memfileMappings))),
		attribute.Int64("snapshot.rootfs.header.mappings.length", int64(len(rootfsMappings))),
		attribute.Int64("snapshot.memfile.diff.size", int64(memfileDirtyPages.Count()*uint(originalMemfile.Header().Metadata.BlockSize))),
		attribute.Int64("snapshot.memfile.mapped_size", int64(memfileMetadata.Size)),
		attribute.Int64("snapshot.memfile.block_size", int64(memfileMetadata.BlockSize)),
		attribute.Int64("snapshot.rootfs.diff.size", int64(rootfsDirtyBlocks.Count()*uint(originalRootfs.Header().Metadata.BlockSize))),
		attribute.Int64("snapshot.rootfs.mapped_size", int64(rootfsMetadata.Size)),
		attribute.Int64("snapshot.rootfs.block_size", int64(rootfsMetadata.BlockSize)),
		attribute.Int64("snapshot.metadata.version", int64(memfileMetadata.Version)),
		attribute.Int64("snapshot.metadata.generation", int64(memfileMetadata.Generation)),
		attribute.String("snapshot.metadata.build_id", memfileMetadata.BuildId.String()),
		attribute.String("snapshot.metadata.base_build_id", memfileMetadata.BaseBuildId.String()),
	)

	return &Snapshot{
		Snapfile:          snapfile,
		MemfileDiff:       memfileDiff,
		MemfileDiffHeader: header.NewHeader(memfileMetadata, memfileMappings),
		RootfsDiff:        rootfsDiff,
		RootfsDiffHeader:  header.NewHeader(rootfsMetadata, rootfsMappings),
	}, nil
}

type Snapshot struct {
	MemfileDiff       build.Diff
	MemfileDiffHeader *header.Header
	RootfsDiff        build.Diff
	RootfsDiffHeader  *header.Header
	Snapfile          *template.LocalFile
}
