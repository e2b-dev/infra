package sandbox

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/bits-and-blooms/bitset"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/mod/semver"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/dns"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/build"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/fc"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/network"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/rootfs"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/stats"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/template"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/logs"
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
	Logger *logs.SandboxLogger
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
	logger *logs.SandboxLogger,
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

	readonlyRootfs, err := t.Rootfs()
	if err != nil {
		return nil, cleanup, fmt.Errorf("failed to get rootfs: %w", err)
	}

	internalLogger := logger.GetInternalLogger()

	rootfsOverlay, err := rootfs.NewCowDevice(
		internalLogger,
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
			fmt.Fprintf(os.Stderr, "[sandbox %s]: rootfs overlay error: %v\n", config.SandboxId, runErr)
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
			LogsCollectorAddress: logs.CollectorPublicIP,
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

	fcStartErr := fcHandle.Start(uffdStartCtx, tracer, internalLogger)
	if fcStartErr != nil {
		return nil, cleanup, fmt.Errorf("failed to start FC: %w", fcStartErr)
	}

	telemetry.ReportEvent(childCtx, "initialized FC")

	pid, err := fcHandle.Pid()
	if err != nil {
		return nil, cleanup, fmt.Errorf("failed to get FC PID: %w", err)
	}

	sandboxStats := stats.NewHandle(ctx, int32(pid))

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
		stats:          sandboxStats,
		Logger:         logger,
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

// Snapshot creates a snapshot of the sandbox
// FC Snapshotting: https://github.com/firecracker-microvm/firecracker/blob/main/docs/snapshotting/snapshot-support.md
// The snapshotting works in three steps:
//  1. Creates a snapshot of the memfile
//     - It pauses the VM
//     - Stops the uffd and starts returnin nil for all requested pages (because full VM snapshot requests all of them)
//     - Creates a VM snapshot
//     - Creates a diff of the memfile excluding the pages tried to be accessed during the snapshotting
//  2. Creates a snapshot of the rootfs
//     - Flushes the NBD device to the local NBD backend
//     - Exports the dirty blocks from the COW device
//  3. Prepares diffs of changes for later use and returns them
func (s *Sandbox) Snapshot(
	ctx context.Context,
	tracer trace.Tracer,
	snapshotTemplateFiles *storage.TemplateCacheFiles,
	releaseLock func(),
) (*Snapshot, error) {
	ctx, childSpan := tracer.Start(ctx, "sandbox-snapshot")
	defer childSpan.End()
	telemetry.ReportEvent(ctx, "starting snapshotting")

	buildId, err := uuid.Parse(snapshotTemplateFiles.BuildId)
	if err != nil {
		return nil, fmt.Errorf("failed to parse build id: %w", err)
	}

	s.healthcheckCtx.Lock()
	s.healthcheckCtx.Cancel()
	s.healthcheckCtx.Unlock()

	// Create snapshot provider that wraps actual implementation
	snapshotProvider := &sandboxSnapshotProvider{
		process: s.process,
		uffd:    s.uffd,
		files:   s.files,
		rootfs:  s.rootfs,
	}

	// Create template provider
	templateProvider := &sandboxTemplateProvider{
		template: s.template,
	}

	return s.createSnapshot(
		ctx,
		tracer,
		buildId,
		snapshotTemplateFiles,
		snapshotProvider,
		templateProvider,
		releaseLock,
	)
}

func (s *Sandbox) createSnapshot(
	ctx context.Context,
	tracer trace.Tracer,
	buildId uuid.UUID,
	snapshotTemplateFiles *storage.TemplateCacheFiles,
	provider SnapshotProvider,
	templateProvider TemplateProvider,
	releaseLock func(),
) (*Snapshot, error) {
	snapFilePath := snapshotTemplateFiles.CacheSnapfilePath()

	// Register and prepare snapfile
	snapfile, err := template.NewLocalFile(snapFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to create local snapfile: %w", err)
	}

	// Create memfile snapshot
	memfileLDFile, memfileDirtyPages, err := s.createMemfileSnapshot(
		ctx,
		tracer,
		buildId,
		snapFilePath,
		snapshotTemplateFiles.CacheMemfileFullSnapshotPath(),
		provider,
		releaseLock,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create memfile snapshot: %w", err)
	}

	// Get original template files
	originalMemfileHeader, err := templateProvider.MemfileHeader()
	if err != nil {
		return nil, fmt.Errorf("failed to get original memfile: %w", err)
	}

	// Create headers and mappings
	memfileHeader := s.createMemfileHeader(
		ctx,
		buildId,
		originalMemfileHeader,
		memfileDirtyPages,
	)

	// Create rootfs snapshot
	rootfsDiffFile, rootfsDirtyBlocks, err := s.createRootfsSnapshot(
		ctx,
		buildId,
		provider,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create rootfs snapshot: %w", err)
	}

	// Get original rootfs files
	originalRootfsHeader, err := templateProvider.RootfsHeader()
	if err != nil {
		return nil, fmt.Errorf("failed to get original rootfs: %w", err)
	}

	// Create rootfs header
	rootfsHeader := s.createRootfsHeader(
		ctx,
		buildId,
		originalRootfsHeader,
		rootfsDirtyBlocks,
	)

	// Create memfile diff
	memfileDiff, err := memfileLDFile.ToDiff(int64(originalMemfileHeader.Metadata.BlockSize))
	if err != nil {
		return nil, fmt.Errorf("failed to convert memfile diff file to local diff: %w", err)
	}
	telemetry.ReportEvent(ctx, "converted memfile diff file to local diff")

	// Create rootfs diff
	rootfsDiff, err := rootfsDiffFile.ToDiff(int64(originalRootfsHeader.Metadata.BlockSize))
	if err != nil {
		return nil, fmt.Errorf("failed to convert rootfs diff file to local diff: %w", err)
	}
	telemetry.ReportEvent(ctx, "converted rootfs diff file to local diff")

	return &Snapshot{
		MemfileDiff:       memfileDiff,
		MemfileDiffHeader: memfileHeader,
		RootfsDiff:        rootfsDiff,
		RootfsDiffHeader:  rootfsHeader,
		Snapfile:          snapfile,
	}, nil
}

func (s *Sandbox) createMemfileSnapshot(
	ctx context.Context,
	tracer trace.Tracer,
	buildId uuid.UUID,
	snapfilePath string,
	memfilePath string,
	provider SnapshotProvider,
	releaseLock func(),
) (*build.LocalDiffFile, *bitset.BitSet, error) {
	defer releaseLock()

	telemetry.ReportEvent(ctx, "pausing vm")
	err := provider.PauseVM(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("error pausing vm: %w", err)
	}

	telemetry.ReportEvent(ctx, "disabling uffd")
	err = provider.DisableUffd()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to disable uffd: %w", err)
	}

	telemetry.ReportEvent(ctx, "creating snapshot")
	err = provider.CreateVMSnapshot(
		ctx,
		tracer,
		snapfilePath,
		memfilePath,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("error creating snapshot: %w", err)
	}

	sourceFile, err := os.Open(memfilePath)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to open memfile: %w", err)
	}
	defer sourceFile.Close()
	defer os.RemoveAll(memfilePath)

	telemetry.ReportEvent(ctx, "uffd dirty")
	memfileDirtyPages := provider.GetDirtyUffd()

	telemetry.ReportEvent(ctx, "create diff")
	memfileLDFile, err := build.NewLocalDiffFile(
		buildId.String(),
		build.Memfile,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create memfile LDFile: %w", err)
	}

	err = header.CreateDiff(sourceFile, provider.GetMemfilePageSize(), memfileDirtyPages, memfileLDFile)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create memfile diff: %w", err)
	}

	return memfileLDFile, memfileDirtyPages, nil
}

func (s *Sandbox) createMemfileHeader(
	ctx context.Context,
	buildId uuid.UUID,
	originalMemfileHeader *header.Header,
	memfileDirtyPages *bitset.BitSet,
) *header.Header {
	telemetry.ReportEvent(ctx, "creating memfile header")
	defer telemetry.ReportEvent(ctx, "done memfile header")

	originalMetadata := originalMemfileHeader.Metadata
	memfileMetadata := &header.Metadata{
		Version:     1,
		Generation:  originalMetadata.Generation + 1,
		BlockSize:   originalMetadata.BlockSize,
		Size:        originalMetadata.Size,
		BuildId:     buildId,
		BaseBuildId: originalMetadata.BaseBuildId,
	}

	memfileMapping := header.CreateMapping(
		memfileMetadata,
		&buildId,
		memfileDirtyPages,
	)

	memfileMappings := header.MergeMappings(
		originalMemfileHeader.Mapping,
		memfileMapping,
	)

	return header.NewHeader(memfileMetadata, memfileMappings)
}

func (s *Sandbox) createRootfsSnapshot(
	ctx context.Context,
	buildId uuid.UUID,
	provider SnapshotProvider,
) (*build.LocalDiffFile, *bitset.BitSet, error) {
	defer telemetry.ReportEvent(ctx, "exported rootfs")

	err := provider.FlushRootfsNBD()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to flush rootfs: %w", err)
	}
	telemetry.ReportEvent(ctx, "synced rootfs")

	rootfsDiffFile, err := build.NewLocalDiffFile(buildId.String(), build.Rootfs)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create rootfs diff: %w", err)
	}
	rootfsDirtyBlocks, err := provider.ExportRootfs(ctx, rootfsDiffFile, s.Stop)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to export rootfs: %w", err)
	}

	return rootfsDiffFile, rootfsDirtyBlocks, nil
}

func (s *Sandbox) createRootfsHeader(
	ctx context.Context,
	buildId uuid.UUID,
	originalRootfsHeader *header.Header,
	rootfsDirtyBlocks *bitset.BitSet,
) *header.Header {
	telemetry.ReportEvent(ctx, "creating rootfs header")
	defer telemetry.ReportEvent(ctx, "done rootfs header")

	originalMetadata := originalRootfsHeader.Metadata
	rootfsMetadata := &header.Metadata{
		Version:     1,
		Generation:  originalMetadata.Generation + 1,
		BlockSize:   originalMetadata.BlockSize,
		Size:        originalMetadata.Size,
		BuildId:     buildId,
		BaseBuildId: originalMetadata.BaseBuildId,
	}

	rootfsMapping := header.CreateMapping(
		rootfsMetadata,
		&buildId,
		rootfsDirtyBlocks,
	)

	rootfsMappings := header.MergeMappings(
		originalRootfsHeader.Mapping,
		rootfsMapping,
	)

	return header.NewHeader(rootfsMetadata, rootfsMappings)
}

type Snapshot struct {
	MemfileDiff       build.Diff
	MemfileDiffHeader *header.Header
	RootfsDiff        build.Diff
	RootfsDiffHeader  *header.Header
	Snapfile          *template.LocalFile
}
