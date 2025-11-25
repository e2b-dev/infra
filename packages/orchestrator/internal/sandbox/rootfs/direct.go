package rootfs

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"sync/atomic"

	"github.com/edsrzf/mmap-go"
	"go.uber.org/zap"
	"golang.org/x/sys/unix"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/cfg"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

type DirectProvider struct {
	config cfg.BuilderConfig

	header *header.Header

	path      string
	blockSize int64

	// TODO: Remove when the snapshot flow is improved
	finishedOperations chan struct{}
	// TODO: Remove when the snapshot flow is improved
	exporting atomic.Bool

	mmap *mmap.MMap
}

func NewDirectProvider(config cfg.BuilderConfig, rootfs block.ReadonlyDevice, path string) (Provider, error) {
	blockSize := rootfs.BlockSize()

	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, fmt.Errorf("error opening file: %w", err)
	}
	defer f.Close()

	size, err := rootfs.Size()
	if err != nil {
		return nil, fmt.Errorf("error getting size: %w", err)
	}

	mm, err := mmap.MapRegion(f, int(size), unix.PROT_READ|unix.PROT_WRITE, 0, 0)
	if err != nil {
		return nil, fmt.Errorf("error mapping region: %w", err)
	}

	return &DirectProvider{
		config: config,

		header: rootfs.Header(),

		path:      path,
		blockSize: blockSize,

		finishedOperations: make(chan struct{}, 1),

		mmap: &mm,
	}, nil
}

func (o *DirectProvider) Verify(_ context.Context) error {
	// No verification needed for direct provider for now
	return nil
}

func (o *DirectProvider) Start(_ context.Context) error {
	return nil
}

func (o *DirectProvider) ExportDiff(
	ctx context.Context,
	out io.Writer,
	stopSandbox func(context.Context) error,
) (*header.DiffMetadata, error) {
	ctx, childSpan := tracer.Start(ctx, "direct-provider-export")
	defer childSpan.End()

	o.exporting.CompareAndSwap(false, true)

	defer func() {
		err := o.mmap.Unmap()
		if err != nil {
			logger.L().Error(ctx, "error unmapping mmap", zap.Error(err))
		}
	}()

	// the error is already logged in go routine in SandboxCreate handler
	go func() {
		err := stopSandbox(ctx)
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

	m, err := o.exportToDiff(ctx, out)
	if err != nil {
		return nil, fmt.Errorf("error building diff metadata: %w", err)
	}

	if o.config.RootfsChecksumVerification {
		checksums, err := calculateChecksums(ctx, o.path, o.blockSize)
		if err != nil {
			return nil, fmt.Errorf("error calculating checksum: %w", err)
		}

		logger.L().Debug(ctx, "exported rootfs checksum direct",
			zap.String("checksum", hex.EncodeToString(checksums.Checksum[:])),
		)

		m.Checksums = &checksums
	}

	telemetry.ReportEvent(ctx, "cache exported")

	return m, nil
}

func (o *DirectProvider) Close(ctx context.Context) error {
	o.finishedOperations <- struct{}{}

	if !o.exporting.CompareAndSwap(false, true) {
		return nil
	}

	return errors.Join(o.sync(ctx), o.mmap.Unmap())
}

func (o *DirectProvider) Path() (string, error) {
	return o.path, nil
}

func (o *DirectProvider) exportToDiff(ctx context.Context, out io.Writer) (*header.DiffMetadata, error) {
	err := o.sync(ctx)
	if err != nil {
		return nil, fmt.Errorf("error flushing path: %w", err)
	}

	builder := header.NewDiffMetadataBuilder(int64(o.header.Metadata.Size), o.blockSize)

	f, err := os.Open(o.path)
	if err != nil {
		return nil, fmt.Errorf("error opening path: %w", err)
	}
	defer f.Close()

	block := make([]byte, o.blockSize)
	for i := int64(0); i < int64(o.header.Metadata.Size); i += o.blockSize {
		n, err := f.ReadAt(block, i)
		if err != nil {
			return nil, fmt.Errorf("error reading from file: %w", err)
		}

		err = builder.Process(ctx, block[:n], out, i)
		if err != nil {
			return nil, fmt.Errorf("error processing block %d: %w", i, err)
		}
	}

	m, err := builder.Build()
	if err != nil {
		return nil, fmt.Errorf("error building diff metadata: %w", err)
	}

	return m, nil
}

func (o *DirectProvider) sync(ctx context.Context) error {
	err := o.mmap.Flush()
	if err != nil {
		return fmt.Errorf("error flushing mmap: %w", err)
	}

	return flush(ctx, o.path)
}

type FileCtx struct {
	*os.File
}

func (f *FileCtx) ReadAt(_ context.Context, p []byte, off int64) (int, error) {
	return f.File.ReadAt(p, off)
}

func calculateChecksums(ctx context.Context, path string, blockSize int64) (header.Checksums, error) {
	f, err := os.Open(path)
	if err != nil {
		return header.Checksums{}, fmt.Errorf("error opening path: %w", err)
	}
	defer f.Close()

	size, err := f.Stat()
	if err != nil {
		return header.Checksums{}, fmt.Errorf("error getting file size: %w", err)
	}

	fctx := &FileCtx{File: f}

	return CalculateChecksumsReader(ctx, fctx, size.Size(), blockSize)
}
