package rootfs

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"syscall"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
	"lukechampine.com/blake3"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

var tracer = otel.Tracer("github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/rootfs")

type Provider interface {
	Start(ctx context.Context) error
	Close(ctx context.Context) error
	Path() (string, error)
	ExportDiff(ctx context.Context, out io.Writer, closeSandbox func(context.Context) error) (*header.DiffMetadata, error)
	Verify(ctx context.Context) error
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

func CalculateChecksumsReader(ctx context.Context, r storage.ReaderAtCtx, size, blockSize int64) (header.Checksums, error) {
	blocks := header.BlocksOffsets(size, blockSize)
	blockChecksums := make([][sha256.Size]byte, len(blocks))

	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(16)

	for i, blockOffset := range blocks {
		g.Go(func() error {
			blockBuffer := make([]byte, blockSize)
			_, err := r.ReadAt(ctx, blockBuffer, blockOffset)
			if err != nil {
				return fmt.Errorf("error reading block %d: %w", i, err)
			}

			blockChecksums[i] = blake3.Sum256(blockBuffer)

			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return header.Checksums{}, err
	}

	checksum := blake3.New(sha256.Size, nil)
	for _, blockChecksum := range blockChecksums {
		checksum.Write(blockChecksum[:])
	}

	return header.Checksums{
		Checksum:       [sha256.Size]byte(checksum.Sum(nil)),
		BlockChecksums: blockChecksums,
	}, nil
}
