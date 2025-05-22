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
	rootfs   rootfs.Provider
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
	template template.Template,
	sandboxTimeout time.Duration,
	rootfsCachePath string,
	processOptions fc.ProcessOptions,
) (*Sandbox, *Cleanup, error) {
	childCtx, childSpan := tracer.Start(ctx, "new-sandbox")
	defer childSpan.End()

	cleanup := NewCleanup()

	ips, err := getNetworkSlot(childCtx, tracer, networkPool, cleanup)
	if err != nil {
		return nil, cleanup, fmt.Errorf("failed to get network slot: %w", err)
	}

	sandboxFiles := template.Files().NewSandboxFiles(config.SandboxId)
	cleanup.Add(func(ctx context.Context) error {
		filesErr := cleanupFiles(sandboxFiles)
		if filesErr != nil {
			return fmt.Errorf("failed to cleanup files: %w", filesErr)
		}

		return nil
	})

	rootFS, err := template.Rootfs()
	if err != nil {
		return nil, cleanup, fmt.Errorf("failed to get rootfs: %w", err)
	}

	rootfsProvider, err := rootfs.NewDirectProvider(
		tracer,
		rootFS,
		// Populate direct cache directly from the source file
		// This is needed for marking all blocks as dirty and being able to read them directly
		rootfsCachePath,
	)
	if err != nil {
		return nil, cleanup, fmt.Errorf("failed to create rootfs overlay: %w", err)
	}
	cleanup.Add(func(ctx context.Context) error {
		return rootfsProvider.Close(ctx)
	})
	go func() {
		runErr := rootfsProvider.Start(childCtx)
		if runErr != nil {
			zap.L().Error("rootfs overlay error", zap.Error(runErr))
		}
	}()

	memfile, err := template.Memfile()
	if err != nil {
		return nil, cleanup, fmt.Errorf("failed to get memfile: %w", err)
	}

	memfileSize, err := memfile.Size()
	if err != nil {
		return nil, cleanup, fmt.Errorf("failed to get memfile size: %w", err)
	}

	resources := &Resources{
		Slot:     ips,
		rootfs:   rootfsProvider,
		memory:   uffd.NewNoopMemory(memfileSize, memfile.BlockSize()),
		uffdExit: make(chan error, 1),
	}

	/// ==== END of resources initialization ====
	rootfsPath, err := rootfsProvider.Path()
	if err != nil {
		return nil, cleanup, fmt.Errorf("failed to get rootfs path: %w", err)
	}
	fcHandle, err := fc.NewProcess(
		childCtx,
		tracer,
		ips,
		sandboxFiles,
		rootfsPath,
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
		config.SandboxId,
		config.TemplateId,
		config.TeamId,
		config.Vcpu,
		config.RamMb,
		config.HugePages,
		processOptions,
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

		template: template,
		files:    sandboxFiles,
		process:  fcHandle,

		cleanup: cleanup,

		ClickhouseStore: nil,
	}

	sbx.Checks = NewChecks(sbx, "", "")

	cleanup.AddPriority(func(ctx context.Context) error {
		return sbx.Close(ctx, tracer)
	})

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
	rootfsPath, err := rootfsOverlay.Path()
	if err != nil {
		return nil, cleanup, fmt.Errorf("failed to get rootfs path: %w", err)
	}
	fcHandle, fcErr := fc.NewProcess(
		uffdStartCtx,
		tracer,
		ips,
		sandboxFiles,
		rootfsPath,
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

		template: t,
		files:    sandboxFiles,
		process:  fcHandle,

		cleanup: cleanup,

		ClickhouseStore: clickhouseStore,
	}

	// Part of the sandbox as we need to stop Checks before pausing the sandbox
	// This is to prevent race condition of reporting unhealthy sandbox
	sbx.Checks = NewChecks(sbx, useLokiMetrics, useClickhouseMetrics)

	cleanup.AddPriority(func(ctx context.Context) error {
		return sbx.Close(ctx, tracer)
	})

	err = sbx.WaitForEnvd(
		ctx,
		tracer,
	)
	if err != nil {
		return nil, cleanup, fmt.Errorf("failed to wait for sandbox start: %w", err)
	}

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
	_, span := tracer.Start(ctx, "sandbox-close")
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
	childCtx, childSpan := tracer.Start(ctx, "sandbox-snapshot")
	defer childSpan.End()

	buildID, err := uuid.Parse(snapshotTemplateFiles.BuildId)
	if err != nil {
		return nil, fmt.Errorf("failed to parse build id: %w", err)
	}

	// Stop the health check before pausing the VM
	s.Checks.Stop()

	if err := s.process.Pause(childCtx, tracer); err != nil {
		return nil, fmt.Errorf("failed to pause VM: %w", err)
	}

	if err := s.memory.Disable(); err != nil {
		return nil, fmt.Errorf("failed to disable uffd: %w", err)
	}

	// Snapfile is not closed as it's returned and cached for later use (like resume)
	snapfile := template.NewLocalFileLink(snapshotTemplateFiles.CacheSnapfilePath())
	// Memfile is also closed on diff creation processing
	/* The process of snapshotting memory is as follows:
	1. Pause FC via API
	2. Snapshot FC via API—memory dump to “file on disk” that is actually tmpfs, because it is too slow
	3. Create the diff - copy the diff pages from tmpfs to normal disk file
	4. Delete tmpfs file
	5. Unlock so another snapshot can use tmpfs space
	*/
	memfile, err := storage.AcquireTmpMemfile(childCtx, buildID.String())
	if err != nil {
		return nil, fmt.Errorf("failed to acquire memfile snapshot: %w", err)
	}
	// Close the file even if an error occurs
	defer memfile.Close()

	err = s.process.CreateSnapshot(
		childCtx,
		tracer,
		snapfile.Path(),
		memfile.Path(),
	)
	if err != nil {
		return nil, fmt.Errorf("error creating snapshot: %w", err)
	}

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
		childCtx,
		tracer,
		buildID,
		originalMemfile.Header(),
		&MemoryDiffCreator{
			tracer:     tracer,
			memfile:    memfile,
			dirtyPages: s.memory.Dirty(),
			blockSize:  originalMemfile.BlockSize(),
			doneHook: func(ctx context.Context) error {
				return memfile.Close()
			},
		},
	)
	if err != nil {
		return nil, fmt.Errorf("error while post processing: %w", err)
	}

	rootfsDiff, rootfsDiffHeader, err := pauseProcessRootfs(
		childCtx,
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
) (rootfs.Provider, error) {
	_, overlaySpan := tracer.Start(ctx, "create-rootfs-overlay")
	defer overlaySpan.End()

	rootfsOverlay, err := rootfs.NewNBDProvider(
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
) (uffd.MemoryBackend, error) {
	fcUffd, uffdErr := uffd.New(memfile, socketPath, memfile.BlockSize())
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

func (s *Sandbox) WaitForExit(
	ctx context.Context,
	tracer trace.Tracer,
) error {
	ctx, childSpan := tracer.Start(ctx, "sandbox-wait-for-exit")
	defer childSpan.End()

	timeout := time.Until(s.EndAt)

	select {
	case <-time.After(timeout):
		return fmt.Errorf("waiting for exit took too long")
	case <-ctx.Done():
		return nil
	case err := <-s.process.Exit:
		if err == nil {
			return nil
		}
		return fmt.Errorf("fc process exited prematurely: %w", err)
	}
}

func (s *Sandbox) WaitForEnvd(
	ctx context.Context,
	tracer trace.Tracer,
) (e error) {
	ctx, childSpan := tracer.Start(ctx, "sandbox-wait-for-start")
	defer childSpan.End()

	defer func() {
		if e != nil {
			return
		}
		// Update the sandbox as started now
		s.Metadata.StartedAt = time.Now()
	}()
	syncCtx, syncCancel := context.WithCancelCause(ctx)
	defer syncCancel(nil)

	go func() {
		select {
		// Ensure the syncing takes at most envdTimeout seconds.
		case <-time.After(envdTimeout):
			syncCancel(fmt.Errorf("syncing took too long"))
		case <-syncCtx.Done():
			return
		case err := <-s.process.Exit:
			syncCancel(fmt.Errorf("fc process exited prematurely: %w", err))
		}
	}()

	initErr := s.initEnvd(syncCtx, tracer, s.Metadata.Config.EnvVars, s.Metadata.Config.EnvdAccessToken)
	if initErr != nil {
		return fmt.Errorf("failed to init new envd: %w", initErr)
	} else {
		telemetry.ReportEvent(syncCtx, fmt.Sprintf("[sandbox %s]: initialized new envd", s.Metadata.Config.SandboxId))
	}

	return nil
}
