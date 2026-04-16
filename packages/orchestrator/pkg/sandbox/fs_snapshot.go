package sandbox

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/build"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/fc"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/rootfs"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/template"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/uffd"
	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

// PauseFS performs a filesystem-only pause: exports only the rootfs CoW diff
// and discards memory state. Processes are lost but user files are preserved.
//
// The returned FSDiff contains the packed dirty blocks and metadata needed to
// reconstruct the filesystem on resume via ResumeFSSandbox.
func (s *Sandbox) PauseFS(ctx context.Context) (d *FSDiff, e error) {
	ctx, span := tracer.Start(ctx, "sandbox-fs-pause")
	defer span.End()

	cleanup := NewCleanup()
	defer func() {
		if e != nil {
			err := cleanup.Run(ctx)
			e = errors.Join(e, err)
		}
	}()

	s.Checks.Stop()

	// Unmount the OverlayFS + disk B from inside the guest.
	// This syncs writes, pivots back to the base rootfs, and cleanly
	// unmounts disk B's ext4 so the host can safely snapshot its file.
	if err := s.requestEnvdUnmountOverlay(ctx); err != nil {
		logger.L().Warn(ctx, "overlay unmount failed, falling back to sync",
			zap.String("sandbox_id", s.Runtime.SandboxID),
			zap.Error(err))

		if syncErr := s.requestEnvdSync(ctx); syncErr != nil {
			logger.L().Warn(ctx, "envd sync also failed",
				zap.String("sandbox_id", s.Runtime.SandboxID),
				zap.Error(syncErr))
		}
	}

	if err := s.process.Pause(ctx); err != nil {
		return nil, fmt.Errorf("failed to pause VM: %w", err)
	}

	// FC requires a snapshot call to flush the block device, even though
	// we won't use the snapfile for FS-only resume.
	throwawayPaths, err := storage.Paths{BuildID: uuid.New().String()}.Cache(s.config.StorageConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create throwaway paths: %w", err)
	}
	defer throwawayPaths.Close()

	throwawaySnapfile := template.NewLocalFileLink(throwawayPaths.CacheSnapfile())
	defer throwawaySnapfile.Close()

	if err := s.process.CreateSnapshot(ctx, throwawaySnapfile.Path()); err != nil {
		return nil, fmt.Errorf("error creating snapshot for disk flush: %w", err)
	}
	telemetry.ReportEvent(ctx, "disk flushed via snapshot")

	originalRootfs, err := s.Template.Rootfs()
	if err != nil {
		return nil, fmt.Errorf("failed to get original rootfs: %w", err)
	}

	buildID := uuid.New()

	rootfsDiffFile, err := build.NewLocalDiffFile(s.config.DefaultCacheDir, buildID.String(), build.Rootfs)
	if err != nil {
		return nil, fmt.Errorf("failed to create rootfs diff file: %w", err)
	}

	diffCreator := &RootfsDiffCreator{
		rootfs:    s.rootfs,
		closeHook: s.Close,
	}

	rootfsDiffMetadata, err := diffCreator.process(ctx, rootfsDiffFile.File)
	if err != nil {
		closeErr := rootfsDiffFile.Close()
		return nil, fmt.Errorf("error exporting rootfs diff: %w", errors.Join(err, closeErr))
	}
	telemetry.ReportEvent(ctx, "exported rootfs diff for FS-only pause")

	rootfsDiff, err := rootfsDiffFile.CloseToDiff(int64(originalRootfs.Header().Metadata.BlockSize))
	if err != nil {
		return nil, fmt.Errorf("failed to finalize rootfs diff: %w", err)
	}
	cleanup.AddNoContext(ctx, rootfsDiff.Close)

	return &FSDiff{
		RootfsDiff:  rootfsDiff,
		DirtyBitset: rootfsDiffMetadata.Dirty,
		BlockSize:   rootfsDiffMetadata.BlockSize,
		cleanup:     cleanup,
	}, nil
}

// ResumeFSSandbox restores a sandbox from a hidden base snapshot with the
// rootfs pre-populated from a saved FS-only diff.
//
// Flow:
//  1. Create rootfs overlay with base + saved diff (via ImportFromDiff)
//  2. Set up uffd to serve the hidden base memory
//  3. Restore the hidden base snapshot (VM wakes with envd on tmpfs, no ext4)
//  4. Tell envd to mount /dev/vda + pivot_root (ext4 mounts fresh, gets CoW blocks)
//  5. Call /init to configure envd
//
// Processes are gone but user files are intact (~130-150ms total).
func (f *Factory) ResumeFSSandbox(
	ctx context.Context,
	t template.Template,
	fsDiff *FSDiff,
	hiddenBaseSnapfile template.File,
	config *Config,
	runtime RuntimeMetadata,
	startedAt time.Time,
	endAt time.Time,
) (s *Sandbox, e error) {
	ctx, span := tracer.Start(ctx, "resume-fs-sandbox")
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

	lifecycleID := uuid.NewString()
	cleanup.Add(ctx, func(ctx context.Context) error {
		f.Sandboxes.Remove(ctx, runtime.SandboxID, lifecycleID)
		return nil
	})

	sandboxFiles := t.Files().NewSandboxFiles(runtime.SandboxID)
	cleanup.Add(ctx, cleanupFiles(f.config, sandboxFiles))

	ipsPromise := getNetworkSlot(ctx, f.networkPool, cleanup, config.Network)

	rootFS, err := t.Rootfs()
	if err != nil {
		return nil, fmt.Errorf("failed to get rootfs: %w", err)
	}

	diffFile, err := fsDiff.DiffFile()
	if err != nil {
		return nil, fmt.Errorf("failed to open diff file: %w", err)
	}
	defer diffFile.Close()

	rootfsProvider, err := rootfs.NewNBDProviderWithDiff(
		ctx,
		rootFS,
		sandboxFiles.SandboxCacheRootfsPath(f.config.StorageConfig),
		f.devicePool,
		f.featureFlags,
		diffFile,
		fsDiff.DirtyBitset,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create rootfs overlay with diff: %w", err)
	}
	cleanup.Add(ctx, rootfsProvider.Close)
	go func() {
		runErr := rootfsProvider.Start(execCtx)
		if runErr != nil {
			logger.L().Error(ctx, "rootfs overlay error", zap.Error(runErr))
		}
	}()

	// Set up uffd to serve the hidden base memory
	fcUffdPath := sandboxFiles.SandboxUffdSocketPath()
	memfile, err := t.Memfile(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get memfile: %w", err)
	}

	fcUffd := uffd.New(memfile, fcUffdPath)
	if err := serveMemory(execCtx, cleanup, fcUffd, runtime.SandboxID); err != nil {
		return nil, fmt.Errorf("failed to serve memory: %w", err)
	}

	ips, err := ipsPromise.Wait(ctx)
	if err != nil {
		return nil, err
	}

	meta, err := t.Metadata()
	if err != nil {
		return nil, fmt.Errorf("failed to get metadata: %w", err)
	}

	cgroupHandle, cgroupFD := createCgroup(ctx, f.cgroupManager, sandboxFiles.SandboxCgroupName())
	defer releaseCgroupFD(ctx, cgroupHandle, runtime.SandboxID)

	cleanup.Add(ctx, func(ctx context.Context) error {
		return cgroupHandle.Remove(ctx)
	})

	fcHandle, err := fc.NewProcess(
		ctx,
		execCtx,
		f.config,
		ips,
		sandboxFiles,
		config.FirecrackerConfig,
		rootfsProvider,
		fc.RootfsPaths{
			TemplateVersion: meta.Version,
			TemplateID:      config.BaseTemplateID,
			BuildID:         rootFS.Header().Metadata.BaseBuildId.String(),
		},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to init FC: %w", err)
	}

	throttleConfig := featureflags.GetTCPFirewallEgressThrottleConfig(ctx, f.featureFlags)
	driveThrottleConfig := featureflags.GetBlockDriveThrottleConfig(ctx, f.featureFlags)

	resources := &Resources{
		Slot:   ips,
		rootfs: rootfsProvider,
		memory: fcUffd,
	}

	metadata := &Metadata{
		internalConfig: internalConfig{
			EnvdInitRequestTimeout: f.GetEnvdInitRequestTimeout(ctx),
		},
		Config:  config,
		Runtime: runtime,

		startedAt: startedAt,
		endAt:     endAt,
	}

	sbx := &Sandbox{
		LifecycleID: lifecycleID,

		Resources:    resources,
		Metadata:     metadata,
		cgroupHandle: cgroupHandle,

		Template: t,
		config:   f.config,
		files:    sandboxFiles,
		process:  fcHandle,

		cleanup: cleanup,

		exit: exit,
	}

	f.Sandboxes.Insert(ctx, sbx)
	cleanup.Add(ctx, func(ctx context.Context) error {
		f.Sandboxes.MarkStopping(ctx, runtime.SandboxID, sbx.LifecycleID)
		return nil
	})

	samplingInterval := time.Duration(f.featureFlags.IntFlag(execCtx, featureflags.HostStatsSamplingInterval)) * time.Millisecond
	initializeHostStatsCollector(execCtx, sbx, runtime, config, f.hostStatsDelivery, samplingInterval)

	cleanup.Add(ctx, func(ctx context.Context) error {
		if sbx.hostStatsCollector != nil {
			sbx.hostStatsCollector.Stop(ctx)
		}
		return nil
	})

	// Restore from hidden base snapshot: VM wakes with envd on tmpfs,
	// ext4 NOT mounted (no stale metadata).
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
		hiddenBaseSnapfile,
		fcUffd.Ready(),
		config.Envd.AccessToken,
		cgroupFD,
		fc.RateLimiterConfig{
			Ops:       fc.TokenBucketConfig(throttleConfig.Ops),
			Bandwidth: fc.TokenBucketConfig(throttleConfig.Bandwidth),
		},
		fc.RateLimiterConfig{
			Ops:       fc.TokenBucketConfig(driveThrottleConfig.Ops),
			Bandwidth: fc.TokenBucketConfig(driveThrottleConfig.Bandwidth),
		},
	)
	if fcStartErr != nil {
		return nil, fmt.Errorf("failed to restore hidden base: %w", fcStartErr)
	}
	telemetry.ReportEvent(ctx, "hidden base snapshot restored")

	useClickhouseMetrics := f.featureFlags.BoolFlag(ctx, featureflags.MetricsWriteFlag)
	sbx.Checks = NewChecks(sbx, useClickhouseMetrics)

	cleanup.AddPriority(ctx, sbx.Stop)

	// Tell envd to mount the overlay region of /dev/vda.
	// The overlay starts right after the rootfs in the composite device.
	rootfsSize, err := rootFS.Size(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get rootfs size: %w", err)
	}

	if err := sbx.requestEnvdMountOverlay(ctx, rootfsSize); err != nil {
		return nil, fmt.Errorf("mount-overlay failed: %w", err)
	}
	telemetry.ReportEvent(ctx, "overlay mounted with user files")

	// Configure envd (time sync, env vars, etc.)
	if err := sbx.WaitForEnvd(ctx, f.config.EnvdTimeout); err != nil {
		return nil, fmt.Errorf("failed to init envd after FS resume: %w", err)
	}

	f.Sandboxes.MarkRunning(ctx, sbx)

	go sbx.Checks.Start(execCtx)

	go func() {
		defer execSpan.End()

		ctx, span := tracer.Start(execCtx, "sandbox-exit-wait")
		defer span.End()

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
