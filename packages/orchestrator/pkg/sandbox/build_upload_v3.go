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

func (u *uncompressedUploader) Upload(ctx context.Context) ([]byte, []byte, error) {
	memfilePath, err := u.snapshot.MemfileDiff.CachePath()
	if err != nil {
		return nil, nil, fmt.Errorf("error getting memfile diff path: %w", err)
	}

	rootfsPath, err := u.snapshot.RootfsDiff.CachePath()
	if err != nil {
		return nil, nil, fmt.Errorf("error getting rootfs diff path: %w", err)
	}

	// V3 stores no per-build dependencies (serializeV3 ignores the map).
	// Finalize up front so concurrent readers (e.g. UFFD page faults on
	// Resume after pause during Checkpoint) don't block on the channel
	// while data uploads to GCS.
	u.snapshot.MemfileDiffHeader.FinalizeDependencies(nil, nil)
	u.snapshot.RootfsDiffHeader.FinalizeDependencies(nil, nil)

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
