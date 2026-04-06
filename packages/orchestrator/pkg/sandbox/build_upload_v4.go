package sandbox

import (
	"context"
	"fmt"

	"golang.org/x/sync/errgroup"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	headers "github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

// compressedUploader implements BuildUploader for V4 (compressed) builds.
type compressedUploader struct {
	buildUploader

	pending *PendingBuildInfo
	cfg     *storage.CompressConfig
}

func (c *compressedUploader) UploadData(ctx context.Context) error {
	memfilePath, err := diffPath(c.snapshot.MemfileDiff)
	if err != nil {
		return fmt.Errorf("error getting memfile diff path: %w", err)
	}

	rootfsPath, err := diffPath(c.snapshot.RootfsDiff)
	if err != nil {
		return fmt.Errorf("error getting rootfs diff path: %w", err)
	}

	eg, ctx := errgroup.WithContext(ctx)

	if memfilePath != nil {
		localPath := *memfilePath
		eg.Go(func() error {
			ft, checksum, err := c.uploadCompressedFile(ctx, localPath, c.paths.MemfileCompressed(c.cfg.CompressionType()), storage.MemfileObjectType, c.cfg)
			if err != nil {
				return fmt.Errorf("compressed memfile upload: %w", err)
			}

			uncompressedSize, _ := ft.Size()
			c.pending.add(pendingBuildInfoKey(c.paths.BuildID, storage.MemfileName), ft, uncompressedSize, checksum)

			return nil
		})
	}

	if rootfsPath != nil {
		localPath := *rootfsPath
		eg.Go(func() error {
			ft, checksum, err := c.uploadCompressedFile(ctx, localPath, c.paths.RootfsCompressed(c.cfg.CompressionType()), storage.RootFSObjectType, c.cfg)
			if err != nil {
				return fmt.Errorf("compressed rootfs upload: %w", err)
			}

			uncompressedSize, _ := ft.Size()
			c.pending.add(pendingBuildInfoKey(c.paths.BuildID, storage.RootfsName), ft, uncompressedSize, checksum)

			return nil
		})
	}

	c.scheduleAlwaysUploads(eg, ctx)

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
			h := c.snapshot.MemfileDiffHeader.CloneForUpload()

			if err := c.pending.applyToHeader(h, storage.MemfileName); err != nil {
				return fmt.Errorf("apply frames to memfile header: %w", err)
			}

			h.Metadata.Version = headers.MetadataVersionCompressed

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
			h := c.snapshot.RootfsDiffHeader.CloneForUpload()

			if err := c.pending.applyToHeader(h, storage.RootfsName); err != nil {
				return fmt.Errorf("apply frames to rootfs header: %w", err)
			}

			h.Metadata.Version = headers.MetadataVersionCompressed

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
