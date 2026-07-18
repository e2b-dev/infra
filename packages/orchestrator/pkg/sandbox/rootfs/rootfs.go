//go:build linux

package rootfs

import (
	"context"
	"fmt"
	"os"
	"syscall"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

var tracer = otel.Tracer("github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/rootfs")

type Provider interface {
	Start(ctx context.Context) error
	Close(ctx context.Context) error
	Path() (string, error)
	ExportDiff(ctx context.Context, out *os.File, closeSandbox func(context.Context) error) (*header.DiffMetadata, error)
	ExportDiffInPlace(ctx context.Context, out *os.File) (*header.DiffMetadata, error)
	// SwapForBackgroundSeal flushes the device, swaps a fresh writable cache onto
	// the live overlay and returns the previous (now frozen) cache so the caller
	// can seal it (ExportToDiff) in the background while the VM keeps running.
	// Only the NBD provider supports it.
	SwapForBackgroundSeal(ctx context.Context) (*block.Cache, error)
	// ReleaseSealed detaches the sealing cache once its background seal has
	// completed (and the base device can serve its blocks), returning it for the
	// caller to Close. Returns nil if none is sealing.
	ReleaseSealed() *block.Cache
}

// flush flushes the data to the operating system's buffer.
func flush(ctx context.Context, path string) error {
	ctx, span := tracer.Start(ctx, "flush", trace.WithAttributes(attribute.String("path", path)))
	defer span.End()

	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("failed to open path: %w", err)
	}
	defer func() {
		err := file.Close()
		if err != nil {
			logger.L().Error(ctx, "failed to close path", zap.Error(err))
		}
	}()

	err = syscall.Fsync(int(file.Fd()))
	if err != nil {
		return fmt.Errorf("failed to fsync path: %w", err)
	}

	err = file.Sync()
	if err != nil {
		return fmt.Errorf("failed to sync path: %w", err)
	}

	return nil
}
