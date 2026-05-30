package rootfs

import (
	"context"
	"errors"
	"fmt"
	"go.uber.org/zap"
	"os"

	"github.com/e2b-dev/ublk-go/ublk"
	"golang.org/x/sys/unix"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block"
	ublkpool "github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/ublk"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

type UblkProvider struct {
	ctx     context.Context
	cancel  context.CancelFunc
	overlay *block.Overlay
	backend *ublkBackend
	dev     *ublk.Device
	pool    *ublkpool.DevicePool

	ready              *utils.SetOnce[string]
	finishedOperations chan struct{}
	blockSize          int64
}

func NewUblkProvider(
	ctx context.Context,
	rootfs block.ReadonlyDevice,
	cachePath string,
	pool *ublkpool.DevicePool,
) (Provider, error) {
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

	// Use a background context so the ublk backend outlives the CreateSandbox
	// request context. Only cancelled explicitly in Close().
	runCtx, cancel := context.WithCancel(context.Background())
	return &UblkProvider{
		ctx:                runCtx,
		cancel:             cancel,
		overlay:            overlay,
		backend:            newUblkBackend(runCtx, overlay),
		pool:               pool,
		ready:              utils.NewSetOnce[string](),
		finishedOperations: make(chan struct{}, 1),
		blockSize:          blockSize,
	}, nil
}

func (u *UblkProvider) Start(ctx context.Context) error {
	size, err := u.overlay.Size(ctx)
	if err != nil {
		return u.ready.SetError(err)
	}

	telemetry.ReportEvent(ctx, "creating ublk device")

	dev, err := u.pool.New(ctx, u.backend, uint64(size))
	if err != nil {
		return u.ready.SetError(fmt.Errorf("ublk.New: %w", err))
	}
	u.dev = dev
	telemetry.ReportEvent(ctx, "ublk device created")
	return u.ready.SetValue(dev.Path())
}

func (u *UblkProvider) Path() (string, error) { return u.ready.Wait() }

func (u *UblkProvider) Close(ctx context.Context) error {
	ctx, span := tracer.Start(ctx, "ublk-close")
	defer span.End()

	var errs []error

	err := u.sync(ctx)
	if err != nil {
		errs = append(errs, fmt.Errorf("ublk flush: %w", err))
	}

	if u.dev != nil {
		err = u.pool.Close(ctx, u.dev)
		if err != nil {
			errs = append(errs, fmt.Errorf("ublk close: %w", err))
		}
	}
	u.cancel()

	u.finishedOperations <- struct{}{}

	err = u.overlay.Close()
	if err != nil {
		errs = append(errs, fmt.Errorf("overlay close: %w", err))
	}
	logger.L().Info(ctx, "ublk overlay device released")
	return errors.Join(errs...)
}

func (u *UblkProvider) ExportDiff(
	ctx context.Context, out *os.File,
	closeSandbox func(context.Context) error,
) (*header.DiffMetadata, error) {
	ctx, span := tracer.Start(ctx, "ublk-export")
	defer span.End()

	cache, err := u.overlay.EjectCache()
	if err != nil {
		return nil, fmt.Errorf("eject cache: %w", err)
	}

	go func() {
		err := closeSandbox(ctx)
		if err != nil {
			logger.L().Error(ctx, "stop sandbox on cow export", zap.Error(err))
		}
	}()

	select {
	case <-u.finishedOperations:
	case <-ctx.Done():
		return nil, fmt.Errorf("timeout waiting for ublk device released")
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

func (u *UblkProvider) sync(ctx context.Context) error {
	ctx, span := tracer.Start(ctx, "ublk-sync")
	defer span.End()

	path, err := u.Path()
	if err != nil {
		return fmt.Errorf("failed to get cow path: %w", err)
	}

	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer func() {
		err := file.Close()
		if err != nil {
			logger.L().Error(ctx, "failed to close ublk file", zap.Error(err))
		}
	}()

	if err := unix.IoctlSetInt(int(file.Fd()), unix.BLKFLSBUF, 0); err != nil {
		return fmt.Errorf("ioctl BLKFLSBUF: %w", err)
	}
	return flush(ctx, path)
}
