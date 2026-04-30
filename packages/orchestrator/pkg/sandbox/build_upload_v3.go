package sandbox

import (
	"context"
	"fmt"

	"golang.org/x/sync/errgroup"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	headers "github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

// uncompressedUploader handles V3 (uncompressed) builds.
type uncompressedUploader struct {
	persistence storage.StorageProvider
	snapshot    *Snapshot
}

func (u *uncompressedUploader) upload(ctx context.Context) (memfileHeader, rootfsHeader []byte, err error) {
	memfilePath, err := u.snapshot.MemfileDiff.CachePath()
	if err != nil {
		return nil, nil, fmt.Errorf("error getting memfile diff path: %w", err)
	}

	rootfsPath, err := u.snapshot.RootfsDiff.CachePath()
	if err != nil {
		return nil, nil, fmt.Errorf("error getting rootfs diff path: %w", err)
	}

	eg, ctx := errgroup.WithContext(ctx)

	eg.Go(func() error {
		if u.snapshot.MemfileDiffHeader == nil {
			return nil
		}

		_, err := headers.StoreHeader(ctx, u.persistence, u.snapshot.Paths.MemfileHeader(), u.snapshot.MemfileDiffHeader)

		return err
	})

	eg.Go(func() error {
		if u.snapshot.RootfsDiffHeader == nil {
			return nil
		}

		_, err := headers.StoreHeader(ctx, u.persistence, u.snapshot.Paths.RootfsHeader(), u.snapshot.RootfsDiffHeader)

		return err
	})

	eg.Go(func() error {
		if memfilePath == "" {
			return nil
		}

		_, _, err := storage.UploadFramed(ctx, u.persistence, u.snapshot.Paths.Memfile(), storage.MemfileObjectType, memfilePath, storage.CompressConfig{})

		return err
	})

	eg.Go(func() error {
		if rootfsPath == "" {
			return nil
		}

		_, _, err := storage.UploadFramed(ctx, u.persistence, u.snapshot.Paths.Rootfs(), storage.RootFSObjectType, rootfsPath, storage.CompressConfig{})

		return err
	})

	eg.Go(func() error {
		return storage.UploadBlob(ctx, u.persistence, u.snapshot.Paths.Snapfile(), storage.SnapfileObjectType, u.snapshot.Snapfile.Path())
	})

	eg.Go(func() error {
		return storage.UploadBlob(ctx, u.persistence, u.snapshot.Paths.Metadata(), storage.MetadataObjectType, u.snapshot.Metafile.Path())
	})

	return nil, nil, eg.Wait()
}
