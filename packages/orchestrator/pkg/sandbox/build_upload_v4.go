package sandbox

import (
	"context"
	"fmt"

	"golang.org/x/sync/errgroup"

	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	headers "github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

// compressedUploader implements BuildUploader for V4 (compressed) builds.
// Per-file config is resolved in UploadData (tier 2) using the upload-level
// config from NewBuildUploader (tier 1) as the base.
type compressedUploader struct {
	buildUploader

	pending *PendingBuildInfo
	cfg     *storage.CompressConfig
	ff      *featureflags.Client // to override cfg on a per-file basis in UploadData
	useCase string
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

	// Tier 2: resolve per file with use-case and file-type context.
	memCfg := storage.ResolveCompressConfig(ctx, c.cfg, c.ff, storage.MemfileName, c.useCase)
	rootfsCfg := storage.ResolveCompressConfig(ctx, c.cfg, c.ff, storage.RootfsName, c.useCase)

	eg, ctx := errgroup.WithContext(ctx)

	if memfilePath != nil {
		localPath := *memfilePath
		eg.Go(func() error {
			if memCfg == nil {
				return c.uploadUncompressedFile(ctx, localPath, c.paths.Memfile(), storage.MemfileObjectType)
			}

			ft, checksum, err := c.uploadCompressedFile(ctx, localPath, c.paths.MemfileCompressed(memCfg.CompressionType()), storage.MemfileObjectType, memCfg)
			if err != nil {
				return fmt.Errorf("compressed memfile upload: %w", err)
			}

			c.pending.add(pendingBuildInfoKey(c.paths.BuildID, storage.MemfileName), ft, ft.UncompressedSize(), checksum)

			return nil
		})
	}

	if rootfsPath != nil {
		localPath := *rootfsPath
		eg.Go(func() error {
			if rootfsCfg == nil {
				return c.uploadUncompressedFile(ctx, localPath, c.paths.Rootfs(), storage.RootFSObjectType)
			}

			ft, checksum, err := c.uploadCompressedFile(ctx, localPath, c.paths.RootfsCompressed(rootfsCfg.CompressionType()), storage.RootFSObjectType, rootfsCfg)
			if err != nil {
				return fmt.Errorf("compressed rootfs upload: %w", err)
			}

			c.pending.add(pendingBuildInfoKey(c.paths.BuildID, storage.RootfsName), ft, ft.UncompressedSize(), checksum)

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
