//go:build linux

package rootfs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/nbd"
	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

type NBDProvider struct {
	overlay      *block.Overlay
	mnt          *nbd.DirectPathMount
	featureFlags *featureflags.Client

	ready *utils.SetOnce[string]

	blockSize int64

	finishedOperations chan struct{}
	devicePool         *nbd.DevicePool
}

func NewNBDProvider(ctx context.Context, rootfs block.ReadonlyDevice, cachePath string, devicePool *nbd.DevicePool, featureFlags *featureflags.Client) (Provider, error) {
	size, err := rootfs.Size(ctx)
	if err != nil {
		return nil, fmt.Errorf("error getting device size: %w", err)
	}

	blockSize := rootfs.BlockSize()

	cache, err := block.NewCache(size, blockSize, cachePath, false)
	if err != nil {
		return nil, fmt.Errorf("error creating cache: %w", err)
	}

	overlay := block.NewOverlay(rootfs, cache)

	mnt := nbd.NewDirectPathMount(overlay, devicePool, featureFlags)

	return &NBDProvider{
		mnt:                mnt,
		overlay:            overlay,
		featureFlags:       featureFlags,
		ready:              utils.NewSetOnce[string](),
		finishedOperations: make(chan struct{}, 1),
		blockSize:          blockSize,
		devicePool:         devicePool,
	}, nil
}

func (o *NBDProvider) Start(ctx context.Context) error {
	deviceIndex, err := o.mnt.Open(ctx)
	if err != nil {
		return o.ready.SetError(fmt.Errorf("error opening overlay file: %w", err))
	}

	return o.ready.SetValue(nbd.GetDevicePath(deviceIndex))
}

func (o *NBDProvider) ExportDiff(
	ctx context.Context,
	out *os.File,
	closeSandbox func(ctx context.Context) error,
	recoverJournal bool,
) (*header.DiffMetadata, error) {
	ctx, span := tracer.Start(ctx, "cow-export")
	defer span.End()

	cache, err := o.overlay.EjectCache()
	if err != nil {
		return nil, fmt.Errorf("error ejecting cache: %w", err)
	}

	// the error is already logged in go routine in SandboxCreate handler
	go func() {
		err := closeSandbox(ctx)
		if err != nil {
			logger.L().Error(ctx, "error stopping sandbox on cow export", zap.Error(err))
		}
	}()

	select {
	case <-o.finishedOperations:
	case <-ctx.Done():
		// Close the cache to avoid leaking the mmaped memory. Log an error
		// if that failed
		closeErr := cache.Close()
		if closeErr != nil {
			logger.L().Warn(ctx, "error closing cache", zap.Error(closeErr))
		}

		return nil, errors.New("timeout waiting for overlay device to be released")
	}
	telemetry.ReportEvent(ctx, "sandbox stopped")

	// The VM is stopped and the original overlay mount is closed, so the ejected
	// cache is now frozen. For a filesystem-only pause on a guest without FIFREEZE
	// support the guest was only sync'd, which can leave a torn ext4 journal in
	// the cache; recover it here so the exported snapshot is clean and mountable.
	// The recovery writes land in the cache and are captured by ExportToDiff below.
	if recoverJournal {
		// Best-effort, mirroring the reboot path: the sandbox is already closed by
		// the time we get here, so a failed e2fsck (timeout, missing binary, slow
		// full check over NBD) must not destroy it with no snapshot. needs_recovery
		// is set for every non-FIFREEZE snapshot, not only torn ones, and the
		// resume-path recovery heals a still-torn journal on the next cold boot.
		// Export what we captured.
		if err := o.recoverJournalOnOverlay(ctx); err != nil {
			logger.L().Warn(ctx, "ext4 journal recovery before snapshot export failed; exporting anyway", zap.Error(err))
		}
	}

	m, err := cache.ExportToDiff(ctx, out)
	if err != nil {
		// Close the cache to avoid leaking the mmaped memory. Log an error
		// if that failed
		closeErr := cache.Close()
		if closeErr != nil {
			logger.L().Warn(ctx, "error closing cache", zap.Error(closeErr))
		}

		return nil, fmt.Errorf("error exporting cache: %w", err)
	}

	telemetry.ReportEvent(ctx, "cache exported")

	err = cache.Close()
	if err != nil {
		return nil, fmt.Errorf("error closing cache: %w", err)
	}

	return m, nil
}

// nbdRecoveryCloseTimeout bounds releasing the temporary journal-recovery NBD
// device. The release path retries a busy device indefinitely on its context, so
// cap it rather than let a stuck disconnect block the (best-effort) pause export
// forever while holding the ejected cache/NBD slot.
const nbdRecoveryCloseTimeout = 30 * time.Second

// recoverJournalOnOverlay re-attaches the (base + ejected cache) overlay to a
// fresh NBD device and runs an ext4 journal recovery on it, so recovery writes
// land in the cache before the diff is exported. Safe to call only after the VM
// is stopped and the original mount is closed (nothing else touches the overlay).
// A no-op when the captured filesystem is already clean.
func (o *NBDProvider) recoverJournalOnOverlay(ctx context.Context) error {
	ctx, span := tracer.Start(ctx, "cow-journal-recovery")
	defer span.End()

	// Isolate the whole recovery from the pause request's cancellation and bound
	// it: the NBD attach, e2fsck, and the flush-into-cache must not be interrupted
	// mid-write by a pause timeout — that would tear down the NBD handlers or skip
	// the flush and leave a half-repaired rootfs that the (best-effort) export then
	// persists. Same isolation as the reboot path.
	recCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), JournalRecoveryTimeout)
	defer cancel()

	mnt := nbd.NewDirectPathMount(o.overlay, o.devicePool, o.featureFlags)
	deviceIndex, err := mnt.Open(recCtx)
	if err != nil {
		return fmt.Errorf("attach overlay for journal recovery: %w", err)
	}
	defer func() {
		// Detach from the pause request's cancellation so the NBD slot is released
		// even if recCtx's own timeout already fired, but keep it bounded: the
		// release path retries device-busy indefinitely on the context, so an
		// unbounded context could block the pause path forever holding the slot.
		closeCtx, closeCancel := context.WithTimeout(context.WithoutCancel(ctx), nbdRecoveryCloseTimeout)
		defer closeCancel()
		if closeErr := mnt.Close(closeCtx); closeErr != nil {
			logger.L().Warn(ctx, "error closing journal-recovery mount", zap.Error(closeErr))
		}
	}()

	devicePath := nbd.GetDevicePath(deviceIndex)
	recovered, err := RecoverExt4Journal(recCtx, devicePath)
	if err != nil {
		return err
	}

	if recovered {
		// Flush the NBD writes into the cache before the mount is torn down so the
		// export sees the recovered blocks.
		if flushErr := mnt.Flush(recCtx); flushErr != nil {
			return fmt.Errorf("flush after journal recovery: %w", flushErr)
		}
		logger.L().Info(ctx, "recovered torn rootfs journal before filesystem-only snapshot export")
	}

	return nil
}

func (o *NBDProvider) Close(ctx context.Context) error {
	ctx, span := tracer.Start(ctx, "cow-close")
	defer span.End()

	var errs []error

	err := o.sync(ctx)
	if err != nil {
		errs = append(errs, fmt.Errorf("error flushing cow device: %w", err))
	}

	err = o.mnt.Close(ctx)
	if err != nil {
		errs = append(errs, fmt.Errorf("error closing overlay mount: %w", err))
	}

	o.finishedOperations <- struct{}{}

	err = o.overlay.Close()
	if err != nil {
		errs = append(errs, fmt.Errorf("error closing overlay cache: %w", err))
	}

	logger.L().Info(ctx, "overlay device released")

	return errors.Join(errs...)
}

func (o *NBDProvider) Path() (string, error) {
	return o.ready.Wait()
}

func (o *NBDProvider) sync(ctx context.Context) error {
	ctx, span := tracer.Start(ctx, "sync")
	defer span.End()

	if _, err := o.Path(); err != nil {
		return fmt.Errorf("failed to get cow path: %w", err)
	}

	return o.mnt.Flush(ctx)
}
