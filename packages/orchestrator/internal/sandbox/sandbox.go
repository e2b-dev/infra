package sandbox

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	globalconfig "github.com/e2b-dev/infra/packages/orchestrator/internal/config"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/build"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/fc"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/nbd"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/network"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/rootfs"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/template"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/metadata"
	"github.com/e2b-dev/infra/packages/shared/pkg/env"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

var defaultEnvdTimeout = utils.Must(time.ParseDuration(env.GetEnv("ENVD_TIMEOUT", "10s")))

var httpClient = http.Client{
	Timeout: 10 * time.Second,
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

	AllowInternetAccess *bool

	Envd EnvdMetadata
}

type EnvdMetadata struct {
	Vars        map[string]string
	AccessToken *string
	Version     string
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

type Metadata struct {
	Config  Config
	Runtime RuntimeMetadata

	StartedAt time.Time
	EndAt     time.Time
}

type Sandbox struct {
	*Resources
	*Metadata

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

// CreateSandbox creates the sandbox.
// IMPORTANT: You must Close() the sandbox after you are done with it.
func CreateSandbox(
	ctx context.Context,
	networkPool *network.Pool,
	devicePool *nbd.DevicePool,
	config Config,
	runtime RuntimeMetadata,
	fcVersions fc.FirecrackerVersions,
	template template.Template,
	sandboxTimeout time.Duration,
	rootfsCachePath string,
	processOptions fc.ProcessOptions,
	apiConfigToStore *orchestrator.SandboxConfig,
) (s *Sandbox, e error) {
	ctx, span := tracer.Start(ctx, "create-sandbox")
	defer span.End()

	runCtx := context.WithoutCancel(ctx)

	exit := utils.NewErrorOnce()

	cleanup := NewCleanup()
	defer func() {
		if e != nil {
			cleanupErr := cleanup.Run(ctx)
			e = errors.Join(e, cleanupErr)
		}
	}()

	allowInternet := globalconfig.AllowSandboxInternet
	if config.AllowInternetAccess != nil {
		allowInternet = *config.AllowInternetAccess
	}

	ipsCh := getNetworkSlotAsync(ctx, networkPool, cleanup, allowInternet)
	defer func() {
		// Ensure the slot is received from chan so the slot is cleaned up properly in cleanup
		<-ipsCh
	}()

	sandboxFiles := template.Files().NewSandboxFiles(runtime.SandboxID)
	cleanup.Add(func(ctx context.Context) error {
		filesErr := cleanupFiles(sandboxFiles)
		if filesErr != nil {
			return fmt.Errorf("failed to cleanup files: %w", filesErr)
		}

		return nil
	})

	rootFS, err := template.Rootfs()
	if err != nil {
		return nil, fmt.Errorf("failed to get rootfs: %w", err)
	}

	var rootfsProvider rootfs.Provider
	if rootfsCachePath == "" {
		rootfsProvider, err = rootfs.NewNBDProvider(
			rootFS,
			sandboxFiles.SandboxCacheRootfsPath(),
			devicePool,
		)
	} else {
		rootfsProvider, err = rootfs.NewDirectProvider(
			rootFS,
			// Populate direct cache directly from the source file
			// This is needed for marking all blocks as dirty and being able to read them directly
			rootfsCachePath,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to create rootfs overlay: %w", err)
	}
	cleanup.Add(func(ctx context.Context) error {
		return rootfsProvider.Close(ctx)
	})
	go func() {
		runErr := rootfsProvider.Start(runCtx)
		if runErr != nil {
			zap.L().Error("rootfs overlay error", zap.Error(runErr))
		}
	}()

	memfile, err := template.Memfile()
	if err != nil {
		return nil, fmt.Errorf("failed to get memfile: %w", err)
	}

	memfileSize, err := memfile.Size()
	if err != nil {
		return nil, fmt.Errorf("failed to get memfile size: %w", err)
	}

	// / ==== END of resources initialization ====
	rootfsPath, err := rootfsProvider.Path()
	if err != nil {
		return nil, fmt.Errorf("failed to get rootfs path: %w", err)
	}
	ips := <-ipsCh
	if ips.err != nil {
		return nil, fmt.Errorf("failed to get network slot: %w", ips.err)
	}
	fcHandle, err := fc.NewProcess(
		ctx,
		ips.slot,
		sandboxFiles,
		fcVersions,
		rootfsPath,
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
		Slot:   ips.slot,
		rootfs: rootfsProvider,
		memory: uffd.NewNoopMemory(memfileSize, memfile.BlockSize()),
	}

	metadata := &Metadata{
		Config:  config,
		Runtime: runtime,

		StartedAt: time.Now(),
		EndAt:     time.Now().Add(sandboxTimeout),
	}

	sbx := &Sandbox{
		Resources: resources,
		Metadata:  metadata,

		Template: template,
		files:    sandboxFiles,
		process:  fcHandle,

		cleanup: cleanup,

		APIStoredConfig: apiConfigToStore,

		exit: exit,
	}

	sbx.Checks = NewChecks(sbx, false)

	cleanup.AddPriority(func(ctx context.Context) error {
		// Stop the sandbox first if it is still running, otherwise do nothing
		return sbx.Stop(ctx)
	})

	go func() {
		ctx, span := tracer.Start(runCtx, "sandbox-exit-wait")
		defer span.End()

		// If the process exists, stop the sandbox properly
		fcErr := fcHandle.Exit.Wait()
		err := sbx.Stop(ctx)

		exit.SetError(errors.Join(err, fcErr))
	}()

	return sbx, nil
}

// ResumeSandbox resumes the sandbox from already saved template or snapshot.
// IMPORTANT: You must Close() the sandbox after you are done with it.
func ResumeSandbox(
	ctx context.Context,
	networkPool *network.Pool,
	t template.Template,
	config Config,
	runtime RuntimeMetadata,
	traceID string,
	startedAt time.Time,
	endAt time.Time,
	devicePool *nbd.DevicePool,
	useClickhouseMetrics bool,
	apiConfigToStore *orchestrator.SandboxConfig,
) (s *Sandbox, e error) {
	ctx, span := tracer.Start(ctx, "resume-sandbox")
	defer span.End()

	runCtx := context.WithoutCancel(ctx)
	runCtx, runSpan := tracer.Start(runCtx, "execute sandbox",
		trace.WithNewRoot(),
	)
	defer func() {
		if e != nil {
			runSpan.AddEvent("resume sandbox aborted")
			runSpan.End()
		}
	}()

	span.AddLink(trace.LinkFromContext(runCtx))

	exit := utils.NewErrorOnce()

	cleanup := NewCleanup()
	defer func() {
		if e != nil {
			cleanupErr := cleanup.Run(ctx)
			e = errors.Join(e, cleanupErr)
		}
	}()

	allowInternet := globalconfig.AllowSandboxInternet
	if config.AllowInternetAccess != nil {
		allowInternet = *config.AllowInternetAccess
	}

	ipsCh := getNetworkSlotAsync(ctx, networkPool, cleanup, allowInternet)
	defer func() {
		// Ensure the slot is received from chan so the slot is cleaned up properly in cleanup
		<-ipsCh
	}()

	sandboxFiles := t.Files().NewSandboxFiles(runtime.SandboxID)
	cleanup.Add(func(ctx context.Context) error {
		filesErr := cleanupFiles(sandboxFiles)
		if filesErr != nil {
			return fmt.Errorf("failed to cleanup files: %w", filesErr)
		}

		return nil
	})

	telemetry.ReportEvent(ctx, "created sandbox files")

	readonlyRootfs, err := t.Rootfs()
	if err != nil {
		return nil, fmt.Errorf("failed to get rootfs: %w", err)
	}

	telemetry.ReportEvent(ctx, "got template rootfs")

	rootfsOverlay, err := rootfs.NewNBDProvider(
		readonlyRootfs,
		sandboxFiles.SandboxCacheRootfsPath(),
		devicePool,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create rootfs overlay: %w", err)
	}

	cleanup.Add(func(ctx context.Context) error {
		return rootfsOverlay.Close(ctx)
	})

	telemetry.ReportEvent(ctx, "created rootfs overlay")

	go func() {
		runErr := rootfsOverlay.Start(runCtx)
		if runErr != nil {
			zap.L().Error("rootfs overlay error", zap.Error(runErr))
		}
	}()

	memfile, err := t.Memfile()
	if err != nil {
		return nil, fmt.Errorf("failed to get memfile: %w", err)
	}

	telemetry.ReportEvent(ctx, "got template memfile")

	fcUffdPath := sandboxFiles.SandboxUffdSocketPath()

	fcUffd, err := serveMemory(
		runCtx,
		cleanup,
		memfile,
		fcUffdPath,
		runtime.SandboxID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to serve memory: %w", err)
	}

	// ==== END of resources initialization ====
	uffdStartCtx, cancelUffdStartCtx := context.WithCancelCause(ctx)
	defer cancelUffdStartCtx(fmt.Errorf("uffd finished starting"))

	go func() {
		uffdWaitErr := fcUffd.Exit().Wait()

		cancelUffdStartCtx(fmt.Errorf("uffd process exited: %w", errors.Join(uffdWaitErr, context.Cause(uffdStartCtx))))
	}()

	rootfsPath, err := rootfsOverlay.Path()
	if err != nil {
		return nil, fmt.Errorf("failed to get rootfs path: %w", err)
	}

	telemetry.ReportEvent(ctx, "got rootfs path")

	ips := <-ipsCh
	if ips.err != nil {
		return nil, fmt.Errorf("failed to get network slot: %w", ips.err)
	}

	telemetry.ReportEvent(ctx, "got network slot")

	meta, err := t.Metadata()
	if err != nil {
		return nil, fmt.Errorf("failed to get metadata: %w", err)
	}

	telemetry.ReportEvent(ctx, "got metadata")

	fcHandle, fcErr := fc.NewProcess(
		uffdStartCtx,
		ips.slot,
		sandboxFiles,
		// The versions need to base exactly the same as the paused sandbox template because of the FC compatibility.
		fc.FirecrackerVersions{
			KernelVersion:      sandboxFiles.KernelVersion,
			FirecrackerVersion: sandboxFiles.FirecrackerVersion,
		},
		rootfsPath,
		fc.RootfsPaths{
			TemplateVersion: meta.Version,
			TemplateID:      config.BaseTemplateID,
			BuildID:         readonlyRootfs.Header().Metadata.BaseBuildId.String(),
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

	fcStartErr := fcHandle.Resume(
		uffdStartCtx,
		&fc.MmdsMetadata{
			SandboxID:  runtime.SandboxID,
			TemplateID: runtime.TemplateID,
			TeamID:     runtime.TeamID,
			TraceID:    traceID,

			LogsCollectorAddress: fmt.Sprintf("http://%s/logs", ips.slot.HyperloopIPString()),
		},
		fcUffdPath,
		snapfile,
		fcUffd.Ready(),
	)
	if fcStartErr != nil {
		return nil, fmt.Errorf("failed to start FC: %w", fcStartErr)
	}

	telemetry.ReportEvent(ctx, "initialized FC")

	resources := &Resources{
		Slot:   ips.slot,
		rootfs: rootfsOverlay,
		memory: fcUffd,
	}

	metadata := &Metadata{
		Config:  config,
		Runtime: runtime,

		StartedAt: startedAt,
		EndAt:     endAt,
	}

	sbx := &Sandbox{
		Resources: resources,
		Metadata:  metadata,

		Template: t,
		files:    sandboxFiles,
		process:  fcHandle,

		cleanup: cleanup,

		APIStoredConfig: apiConfigToStore,

		exit: exit,
	}

	// Part of the sandbox as we need to stop Checks before pausing the sandbox
	// This is to prevent race condition of reporting unhealthy sandbox
	sbx.Checks = NewChecks(sbx, useClickhouseMetrics)

	cleanup.AddPriority(func(ctx context.Context) error {
		// Stop the sandbox first if it is still running, otherwise do nothing
		return sbx.Stop(ctx)
	})

	err = sbx.WaitForEnvd(
		ctx,
		defaultEnvdTimeout,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to wait for sandbox start: %w", err)
	}

	go sbx.Checks.Start(runCtx)

	go func() {
		defer runSpan.End()

		ctx, span := tracer.Start(runCtx, "sandbox-exit-wait")
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
	_, span := tracer.Start(ctx, "sandbox-close")
	defer span.End()

	var errs []error

	// Stop the health checks before stopping the sandbox
	s.Checks.Stop()

	fcStopErr := s.process.Stop()
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

	snapshotTemplateFiles, err := m.Template.CacheFiles()
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
	snapfile := template.NewLocalFileLink(snapshotTemplateFiles.CacheSnapfilePath())
	// Memfile is also closed on diff creation processing
	/* The process of snapshotting memory is as follows:
	1. Pause FC via API
	2. Snapshot FC via API—memory dump to “file on disk” that is actually tmpfs, because it is too slow
	3. Create the diff - copy the diff pages from tmpfs to normal disk file
	4. Delete tmpfs file
	5. Unlock so another snapshot can use tmpfs space
	*/
	memfile, err := storage.AcquireTmpMemfile(ctx, buildID.String())
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
	originalMemfile, err := s.Template.Memfile()
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
			doneHook: func(ctx context.Context) error {
				return memfile.Close()
			},
		},
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
	)
	if err != nil {
		return nil, fmt.Errorf("error while post processing: %w", err)
	}

	metadataFileLink := template.NewLocalFileLink(snapshotTemplateFiles.CacheMetadataPath())
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
) (build.Diff, *header.Header, error) {
	ctx, span := tracer.Start(ctx, "process-memory")
	defer span.End()

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

	memfileHeader, err := header.NewHeader(memfileMetadata, memfileMappings)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create memfile header: %w", err)
	}

	return memfileDiff, memfileHeader, nil
}

func pauseProcessRootfs(
	ctx context.Context,
	buildId uuid.UUID,
	originalHeader *header.Header,
	diffCreator DiffCreator,
) (build.Diff, *header.Header, error) {
	ctx, span := tracer.Start(ctx, "process-rootfs")
	defer span.End()

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

	rootfsHeader, err := header.NewHeader(rootfsMetadata, rootfsMappings)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create rootfs header: %w", err)
	}

	return rootfsDiff, rootfsHeader, nil
}

func getNetworkSlotAsync(
	ctx context.Context,
	networkPool *network.Pool,
	cleanup *Cleanup,
	allowInternet bool,
) chan networkSlotRes {
	ctx, span := tracer.Start(ctx, "get-network-slot")
	defer span.End()

	r := make(chan networkSlotRes, 1)

	go func() {
		defer close(r)

		ips, err := networkPool.Get(ctx, allowInternet)
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
	socketPath string,
	sandboxID string,
) (uffd.MemoryBackend, error) {
	ctx, span := tracer.Start(ctx, "serve-memory")
	defer span.End()

	fcUffd, uffdErr := uffd.New(memfile, socketPath, memfile.BlockSize())
	if uffdErr != nil {
		return nil, fmt.Errorf("failed to create uffd: %w", uffdErr)
	}

	uffdStartErr := fcUffd.Start(ctx, sandboxID)
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
	ctx, span := tracer.Start(ctx, "sandbox-wait-for-start")
	defer span.End()

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
		// Ensure the syncing takes at most timeout seconds.
		case <-time.After(timeout):
			syncCancel(fmt.Errorf("syncing took too long"))
		case <-syncCtx.Done():
			return
		case <-s.process.Exit.Done():
			err := s.process.Exit.Error()

			syncCancel(fmt.Errorf("fc process exited prematurely: %w", err))
		}
	}()

	initErr := s.initEnvd(syncCtx, s.Config.Envd.Vars, s.Config.Envd.AccessToken)
	if initErr != nil {
		return fmt.Errorf("failed to init new envd: %w", initErr)
	}

	telemetry.ReportEvent(syncCtx, fmt.Sprintf("[sandbox %s]: initialized new envd", s.Metadata.Runtime.SandboxID))

	return nil
}
