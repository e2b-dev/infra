package sandbox

import (
	"context"
	"fmt"

	"golang.org/x/sync/errgroup"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

type uncompressedUploader struct {
	buildUploader
}

func (u *uncompressedUploader) Upload(ctx context.Context) ([]byte, []byte, error) {
	// Release self-Pending on V3 headers so descendant WaitForDependencies
	// don't leak. V3 carries no FrameTable, so zero Dep is sufficient.
	// Done up front so every error path below still resolves the Pending.
	_ = u.snapshot.MemfileDiffHeader.Finalize(header.Dependency{})
	_ = u.snapshot.RootfsDiffHeader.Finalize(header.Dependency{})

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
		h := u.snapshot.MemfileDiffHeader
		if h == nil {
			return nil
		}
		data, err := h.SerializeV3()
		if err != nil {
			return fmt.Errorf("serialize memfile header: %w", err)
		}
		blob, err := u.persistence.OpenBlob(ctx, u.paths.MemfileHeader(), storage.MetadataObjectType)
		if err != nil {
			return fmt.Errorf("open memfile header blob: %w", err)
		}

		return blob.Put(ctx, data)
	})

	eg.Go(func() error {
		h := u.snapshot.RootfsDiffHeader
		if h == nil {
			return nil
		}
		data, err := h.SerializeV3()
		if err != nil {
			return fmt.Errorf("serialize rootfs header: %w", err)
		}
		blob, err := u.persistence.OpenBlob(ctx, u.paths.RootfsHeader(), storage.MetadataObjectType)
		if err != nil {
			return fmt.Errorf("open rootfs header blob: %w", err)
		}

		return blob.Put(ctx, data)
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
