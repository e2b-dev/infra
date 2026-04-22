package sandbox

import (
	"context"
	"fmt"

	"golang.org/x/sync/errgroup"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	headers "github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

type uncompressedUploader struct {
	buildUploader
}

func (u *uncompressedUploader) Upload(ctx context.Context) (_, _ []byte, e error) {
	memfilePath, err := u.snapshot.MemfileDiff.CachePath()
	if err != nil {
		return nil, nil, fmt.Errorf("error getting memfile diff path: %w", err)
	}

	rootfsPath, err := u.snapshot.RootfsDiff.CachePath()
	if err != nil {
		return nil, nil, fmt.Errorf("error getting rootfs diff path: %w", err)
	}

	// V3 has no per-build dependencies. Unblock waiters on success;
	// propagate the upload error on failure so children don't proceed
	// against incomplete data.
	defer func() {
		u.snapshot.MemfileDiffHeader.FinalizeDependencies(nil, e)
		u.snapshot.RootfsDiffHeader.FinalizeDependencies(nil, e)
	}()

	eg, ctx := errgroup.WithContext(ctx)

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

	return nil, nil, eg.Wait()
}

var _ BuildUploader = (*uncompressedUploader)(nil)
