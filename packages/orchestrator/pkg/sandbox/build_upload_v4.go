package sandbox

import (
	"context"
	"fmt"

	"golang.org/x/sync/errgroup"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	headers "github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

// compressedUploader implements BuildUploader for V4 (compressed) builds.
// Per-file configs are resolved in NewBuildUploader and passed in directly.
type compressedUploader struct {
	buildUploader

	pending   *PendingBuildInfo
	memCfg    storage.CompressConfig
	rootfsCfg storage.CompressConfig
}

func (c *compressedUploader) UploadData(ctx context.Context) error {
	memfilePath, err := c.snapshot.MemfileDiff.CachePath()
	if err != nil {
		return fmt.Errorf("error getting memfile diff path: %w", err)
	}

	rootfsPath, err := c.snapshot.RootfsDiff.CachePath()
	if err != nil {
		return fmt.Errorf("error getting rootfs diff path: %w", err)
	}

	eg, ctx := errgroup.WithContext(ctx)

	if memfilePath != "" {
		eg.Go(func() error {
			if !c.memCfg.IsCompressionEnabled() {
				_, _, err := storage.UploadFramed(ctx, c.persistence, c.paths.Memfile(), storage.MemfileObjectType, memfilePath, storage.CompressConfig{})

				return err
			}

			ft, checksum, err := storage.UploadFramed(ctx, c.persistence, c.paths.MemfileCompressed(c.memCfg.CompressionType()), storage.MemfileObjectType, memfilePath, c.memCfg)
			if err != nil {
				return fmt.Errorf("compressed memfile upload: %w", err)
			}

			c.pending.add(pendingBuildInfoKey(c.paths.BuildID, storage.MemfileName), ft, ft.UncompressedSize(), checksum)

			return nil
		})
	}

	if rootfsPath != "" {
		eg.Go(func() error {
			if !c.rootfsCfg.IsCompressionEnabled() {
				_, _, err := storage.UploadFramed(ctx, c.persistence, c.paths.Rootfs(), storage.RootFSObjectType, rootfsPath, storage.CompressConfig{})

				return err
			}

			ft, checksum, err := storage.UploadFramed(ctx, c.persistence, c.paths.RootfsCompressed(c.rootfsCfg.CompressionType()), storage.RootFSObjectType, rootfsPath, c.rootfsCfg)
			if err != nil {
				return fmt.Errorf("compressed rootfs upload: %w", err)
			}

			c.pending.add(pendingBuildInfoKey(c.paths.BuildID, storage.RootfsName), ft, ft.UncompressedSize(), checksum)

			return nil
		})
	}

	eg.Go(func() error {
		return storage.UploadBlob(ctx, c.persistence, c.paths.Snapfile(), storage.SnapfileObjectType, c.snapshot.Snapfile.Path())
	})

	eg.Go(func() error {
		return storage.UploadBlob(ctx, c.persistence, c.paths.Metadata(), storage.MetadataObjectType, c.snapshot.Metafile.Path())
	})

	return eg.Wait()
}

// FinalizeHeaders applies pending frame tables to headers and uploads them as V4 format.
//
// The snapshot headers are cloned before mutation because the originals may be
// concurrently read by sandboxes resumed from the template cache (e.g. the
// optimize phase's UFFD handlers).
func (c *compressedUploader) FinalizeHeaders(ctx context.Context) (memfileHeader, rootfsHeader []byte, err error) {
	eg, ctx := errgroup.WithContext(ctx)

	if c.snapshot.MemfileDiffHeader != nil {
		eg.Go(func() error {
			h := c.pending.PrepareV4Header(c.snapshot.MemfileDiffHeader, storage.MemfileName)

			data, err := headers.StoreHeader(ctx, c.persistence, c.paths.MemfileHeader(), h)
			if err != nil {
				return err
			}

			memfileHeader = data

			return nil
		})
	}

	if c.snapshot.RootfsDiffHeader != nil {
		eg.Go(func() error {
			h := c.pending.PrepareV4Header(c.snapshot.RootfsDiffHeader, storage.RootfsName)

			data, err := headers.StoreHeader(ctx, c.persistence, c.paths.RootfsHeader(), h)
			if err != nil {
				return err
			}

			rootfsHeader = data

			return nil
		})
	}

	if err = eg.Wait(); err != nil {
		return nil, nil, err
	}

	return memfileHeader, rootfsHeader, nil
}

// Ensure compressedUploader implements BuildUploader.
var _ BuildUploader = (*compressedUploader)(nil)
