//go:build linux

package rootfs

import (
	"context"
	"errors"
	"fmt"
	"os"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
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
) (*header.DiffMetadata, error) {
	ctx, span := tracer.Start(
		ctx,
		"cow-export",
		trace.WithAttributes(attribute.Bool("in-place", false)),
	)
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

// ExportDiffInPlace exports the NBD cache into `out` without closing/destroying
// the underlying cache
func (o *NBDProvider) ExportDiffInPlace(
	ctx context.Context,
	out *os.File,
) (*header.DiffMetadata, error) {
	ctx, span := tracer.Start(
		ctx,
		"cow-export",
		trace.WithAttributes(attribute.Bool("in-place", true)),
	)
	defer span.End()

	if err := o.sync(ctx); err != nil {
		return nil, fmt.Errorf("flushing COW device failed: %w", err)
	}

	return o.overlay.ExportDiffInPlace(ctx, out)
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
