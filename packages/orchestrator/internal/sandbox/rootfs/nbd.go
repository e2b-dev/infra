package rootfs

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"

	"go.uber.org/zap"
	"golang.org/x/sys/unix"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/cfg"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/nbd"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

type NBDProvider struct {
	config cfg.BuilderConfig

	overlay *block.Overlay
	mnt     *nbd.DirectPathMount

	ready *utils.SetOnce[string]

	blockSize int64

	finishedOperations chan *header.Checksums
	devicePool         *nbd.DevicePool
}

func NewNBDProvider(config cfg.BuilderConfig, rootfs block.ReadonlyDevice, cachePath string, devicePool *nbd.DevicePool) (Provider, error) {
	size, err := rootfs.Size()
	if err != nil {
		return nil, fmt.Errorf("error getting device size: %w", err)
	}

	blockSize := rootfs.BlockSize()

	cache, err := block.NewCache(size, blockSize, cachePath, false)
	if err != nil {
		return nil, fmt.Errorf("error creating cache: %w", err)
	}

	overlay := block.NewOverlay(rootfs, cache)

	mnt := nbd.NewDirectPathMount(overlay, devicePool)

	return &NBDProvider{
		config: config,

		mnt:                mnt,
		overlay:            overlay,
		ready:              utils.NewSetOnce[string](),
		finishedOperations: make(chan *header.Checksums, 1),
		blockSize:          blockSize,
		devicePool:         devicePool,
	}, nil
}

func (o *NBDProvider) Verify(ctx context.Context) error {
	if !o.config.RootfsChecksumVerification {
		return nil
	}

	l := logger.L().With(
		zap.String("checksum_expected", hex.EncodeToString(o.overlay.Header().Checksums.Checksum[:])),
		logger.WithBuildID(o.overlay.Header().Metadata.BuildId.String()),
	)

	l.Debug(ctx, "verifying rootfs checksum nbd")

	checksums, err := o.calculateChecksums(ctx)
	if err != nil {
		return fmt.Errorf("error calculating checksum: %w", err)
	}

	l = l.With(
		zap.String("checksum", hex.EncodeToString(checksums.Checksum[:])),
	)

	if len(checksums.BlockChecksums) != len(o.overlay.Header().Checksums.BlockChecksums) {
		return fmt.Errorf("block checksums length mismatch, expected %d, got %d", len(o.overlay.Header().Checksums.BlockChecksums), len(checksums.BlockChecksums))
	}

	wrongCount := 0
	for blockIndex, blockChecksum := range checksums.BlockChecksums {
		blockOffset := header.BlockOffset(int64(blockIndex), o.blockSize)

		blockChecksumExpected := o.overlay.Header().Checksums.BlockChecksums[blockIndex]
		if !bytes.Equal(blockChecksum[:], blockChecksumExpected[:]) {
			l.Error(ctx, "rootfs block checksum mismatch nbd",
				zap.Int("block_index", blockIndex),
				zap.Int64("block_offset", blockOffset),
				zap.String("block_checksum", hex.EncodeToString(blockChecksum[:])),
				zap.String("block_checksum_expected", hex.EncodeToString(blockChecksumExpected[:])),
			)
			wrongCount++
			if wrongCount > 10 {
				return fmt.Errorf("too many block checksum mismatches")
			}
		}
	}

	if !bytes.Equal(checksums.Checksum[:], o.overlay.Header().Checksums.Checksum[:]) {
		return fmt.Errorf("rootfs checksum mismatch nbd")
	}

	l.Debug(ctx, "rootfs checksum verified nbd", zap.String("checksum", hex.EncodeToString(checksums.Checksum[:])))

	return nil
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

	var checksums *header.Checksums
	select {
	case checksums = <-o.finishedOperations:
	case <-ctx.Done():
		return nil, fmt.Errorf("timeout waiting for overlay device to be released")
	}
	telemetry.ReportEvent(ctx, "sandbox stopped")

	m, err := cache.ExportToDiff(ctx, out)
	if err != nil {
		return nil, fmt.Errorf("error exporting cache: %w", err)
	}

	m.Checksums = checksums

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

	var checksums *header.Checksums
	if o.config.RootfsChecksumVerification {
		c, err := o.calculateChecksums(ctx)
		if err != nil {
			errs = append(errs, fmt.Errorf("error calculating checksum: %w", err))
		}

		checksums = &c
	}

	err = o.mnt.Close(ctx)
	if err != nil {
		errs = append(errs, fmt.Errorf("error closing overlay mount: %w", err))
	}

	o.finishedOperations <- checksums

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

func (o *NBDProvider) calculateChecksums(ctx context.Context) (header.Checksums, error) {
	// go over the whole cache and calculate the checksum, include all blocks
	path, err := o.Path()
	if err != nil {
		return header.Checksums{}, fmt.Errorf("error getting path: %w", err)
	}

	f, err := os.Open(path)
	if err != nil {
		return header.Checksums{}, fmt.Errorf("error opening path: %w", err)
	}
	defer f.Close()

	fctx := &FileCtx{File: f}

	size, err := o.overlay.Size()
	if err != nil {
		return header.Checksums{}, fmt.Errorf("error getting cache size: %w", err)
	}

	return CalculateChecksumsReader(ctx, fctx, size, o.blockSize)
}
