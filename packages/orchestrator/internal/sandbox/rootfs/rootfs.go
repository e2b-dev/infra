package rootfs

import (
	"context"
	"fmt"
	"io"
	"os"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"golang.org/x/sys/unix"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

var tracer = otel.Tracer("github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/rootfs")

type Provider interface {
	Start(ctx context.Context) error
	Close(ctx context.Context) error
	Path() (string, error)
	ExportDiff(ctx context.Context, out io.Writer, closeSandbox func(context.Context) error) (*header.DiffMetadata, error)
}

func Syncfs(fd int) error {
	// SYS_SYNCFS exists on all supported Linux architectures
	_, _, errno := unix.Syscall(unix.SYS_SYNCFS, uintptr(fd), 0, 0)
	if errno != 0 {
		return fmt.Errorf("syncfs failed: %w", errno)
	}

	return nil
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

	// err = syscall.Fsync(int(file.Fd()))
	// if err != nil {
	// 	return fmt.Errorf("failed to fsync path: %w", err)
	// }

	err = Syncfs(int(file.Fd()))
	if err != nil {
		return fmt.Errorf("failed to syncfs path: %w", err)
	}

	err = file.Sync()
	if err != nil {
		return fmt.Errorf("failed to sync path: %w", err)
	}

	return nil
}
