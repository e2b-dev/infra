package rootfs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"syscall"

	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"golang.org/x/sys/unix"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/nbd"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

type NBDProvider struct {
	overlay *block.Overlay
	mnt     *nbd.DirectPathMount

	ready *utils.SetOnce[string]

	blockSize int64

	finishedOperations chan struct{}
	devicePool         *nbd.DevicePool

	tracer trace.Tracer
}

func NewNBDProvider(tracer trace.Tracer, rootfs block.ReadonlyDevice, cachePath string, devicePool *nbd.DevicePool) (Provider, error) {
	size, err := rootfs.Size()
	if err != nil {
		return nil, fmt.Errorf("error getting device size: %w", err)
	}

	blockSize := rootfs.BlockSize()

	cache, err := block.NewCache(size, blockSize, cachePath, false)
	if err != nil {
		return nil, fmt.Errorf("error creating cache: %w", err)
	}

	overlay := block.NewOverlay(rootfs, cache, blockSize)

	mnt := nbd.NewDirectPathMount(tracer, overlay, devicePool)

	return &NBDProvider{
		tracer:             tracer,
		mnt:                mnt,
		overlay:            overlay,
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
	parentCtx context.Context,
	out io.Writer,
	stopSandbox func(ctx context.Context) error,
) (*header.DiffMetadata, error) {
	childCtx, childSpan := o.tracer.Start(parentCtx, "cow-export")
	defer childSpan.End()

	cache, err := o.overlay.EjectCache()
	if err != nil {
		return nil, fmt.Errorf("error ejecting cache: %w", err)
	}

	// the error is already logged in go routine in SandboxCreate handler
	go func() {
		err := stopSandbox(childCtx)
		if err != nil {
			zap.L().Error("error stopping sandbox on cow export", zap.Error(err))
		}
	}()

	select {
	case <-o.finishedOperations:
		break
	case <-childCtx.Done():
		return nil, fmt.Errorf("timeout waiting for overlay device to be released")
	}
	telemetry.ReportEvent(childCtx, "sandbox stopped")

	m, err := cache.ExportToDiff(out)
	if err != nil {
		return nil, fmt.Errorf("error exporting cache: %w", err)
	}

	telemetry.ReportEvent(childCtx, "cache exported")

	err = cache.Close()
	if err != nil {
		return nil, fmt.Errorf("error closing cache: %w", err)
	}

	return m, nil
}

func (o *NBDProvider) Close(ctx context.Context) error {
	childCtx, childSpan := o.tracer.Start(ctx, "cow-close")
	defer childSpan.End()

	var errs []error

	err := o.flush(childCtx)
	if err != nil {
		errs = append(errs, fmt.Errorf("error flushing cow device: %w", err))
	}

	err = o.mnt.Close(childCtx)
	if err != nil {
		errs = append(errs, fmt.Errorf("error closing overlay mount: %w", err))
	}

	o.finishedOperations <- struct{}{}

	err = o.overlay.Close()
	if err != nil {
		errs = append(errs, fmt.Errorf("error closing overlay cache: %w", err))
	}

	zap.L().Info("overlay device released")

	return errors.Join(errs...)
}

func (o *NBDProvider) Path() (string, error) {
	return o.ready.Wait()
}

// flush flushes the data to the operating system's buffer.
func (o *NBDProvider) flush(ctx context.Context) error {
	telemetry.ReportEvent(ctx, "flushing cow device")
	defer telemetry.ReportEvent(ctx, "flushing cow done")

	nbdPath, err := o.Path()
	if err != nil {
		return fmt.Errorf("failed to get cow path: %w", err)
	}

	file, err := os.Open(nbdPath)
	if err != nil {
		return fmt.Errorf("failed to open cow path: %w", err)
	}
	defer func() {
		err := file.Close()
		if err != nil {
			zap.L().Error("failed to close nbd file", zap.Error(err))
		}
	}()

	if err := unix.IoctlSetInt(int(file.Fd()), unix.BLKFLSBUF, 0); err != nil {
		return fmt.Errorf("ioctl BLKFLSBUF failed: %w", err)
	}

	err = syscall.Fsync(int(file.Fd()))
	if err != nil {
		return fmt.Errorf("failed to fsync cow path: %w", err)
	}

	err = file.Sync()
	if err != nil {
		return fmt.Errorf("failed to sync cow path: %w", err)
	}

	return nil
}
