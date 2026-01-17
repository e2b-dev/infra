package rootfs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"go.uber.org/zap"
	"golang.org/x/sys/unix"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/nbd"
	featureflags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
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
	out io.Writer,
	closeSandbox func(ctx context.Context) error,
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
		return nil, fmt.Errorf("timeout waiting for overlay device to be released")
	}
	telemetry.ReportEvent(ctx, "sandbox stopped")

	m, err := cache.ExportToDiff(ctx, out)
	if err != nil {
		return nil, fmt.Errorf("error exporting cache: %w", err)
	}

	telemetry.ReportEvent(ctx, "cache exported")

	err = cache.Close()
	if err != nil {
		return nil, fmt.Errorf("error closing cache: %w", err)
	}

	return m, nil
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

	nbdPath, err := o.Path()
	if err != nil {
		return fmt.Errorf("failed to get cow path: %w", err)
	}

	file, err := os.Open(nbdPath)
	if err != nil {
		return fmt.Errorf("failed to open path: %w", err)
	}
	defer func() {
		err := file.Close()
		if err != nil {
			logger.L().Error(ctx, "failed to close nbd file", zap.Error(err))
		}
	}()

	if err := unix.IoctlSetInt(int(file.Fd()), unix.BLKFLSBUF, 0); err != nil {
		return fmt.Errorf("ioctl BLKFLSBUF failed: %w", err)
	}

	return flush(ctx, nbdPath)
}
