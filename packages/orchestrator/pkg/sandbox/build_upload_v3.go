package sandbox

import (
	"context"
	"fmt"

	"golang.org/x/sync/errgroup"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	headers "github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

// uncompressedUploader implements BuildUploader for V3 (uncompressed) builds.
type uncompressedUploader struct {
	buildUploader
}

func (u *uncompressedUploader) UploadData(ctx context.Context) error {
	memfilePath, err := u.snapshot.MemfileDiff.CachePath()
	if err != nil {
		return fmt.Errorf("error getting memfile diff path: %w", err)
	}

	rootfsPath, err := u.snapshot.RootfsDiff.CachePath()
	if err != nil {
		return fmt.Errorf("error getting rootfs diff path: %w", err)
	}

	eg, ctx := errgroup.WithContext(ctx)

	// V3 headers
	eg.Go(func() error {
		if u.snapshot.MemfileDiffHeader == nil {
			return nil
		}

		_, err := headers.StoreHeader(ctx, u.persistence, u.paths.MemfileHeader(), u.snapshot.MemfileDiffHeader)

		return err
	})

	eg.Go(func() error {
		if u.snapshot.RootfsDiffHeader == nil {
			return nil
		}

		_, err := headers.StoreHeader(ctx, u.persistence, u.paths.RootfsHeader(), u.snapshot.RootfsDiffHeader)

		return err
	})

	// Uncompressed data
	eg.Go(func() error {
		if memfilePath == "" {
			return nil
		}

		_, _, err := storage.UploadFramed(ctx, u.persistence, u.paths.Memfile(), storage.MemfileObjectType, memfilePath, storage.CompressConfig{})

		return err
	})

	eg.Go(func() error {
		if rootfsPath == "" {
			return nil
		}

		_, _, err := storage.UploadFramed(ctx, u.persistence, u.paths.Rootfs(), storage.RootFSObjectType, rootfsPath, storage.CompressConfig{})

		return err
	})

	eg.Go(func() error {
		return storage.UploadBlob(ctx, u.persistence, u.paths.Snapfile(), storage.SnapfileObjectType, u.snapshot.Snapfile.Path())
	})

	eg.Go(func() error {
		return storage.UploadBlob(ctx, u.persistence, u.paths.Metadata(), storage.MetadataObjectType, u.snapshot.Metafile.Path())
	})

	return eg.Wait()
}

func (u *uncompressedUploader) FinalizeHeaders(context.Context) ([]byte, []byte, error) {
	return nil, nil, nil
}

// Ensure uncompressedUploader implements BuildUploader.
var _ BuildUploader = (*uncompressedUploader)(nil)
