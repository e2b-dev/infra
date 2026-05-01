package sandbox

import (
	"context"
	"fmt"

	"golang.org/x/sync/errgroup"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/build"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	headers "github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

func (u *Upload) runV3(ctx context.Context) (memfileHeader, rootfsHeader []byte, err error) {
	memfilePath, err := u.snap.MemfileDiff.CachePath()
	if err != nil {
		return nil, nil, fmt.Errorf("error getting memfile diff path: %w", err)
	}

	rootfsPath, err := u.snap.RootfsDiff.CachePath()
	if err != nil {
		return nil, nil, fmt.Errorf("error getting rootfs diff path: %w", err)
	}

	eg, ctx := errgroup.WithContext(ctx)

	eg.Go(func() error {
		if u.snap.MemfileDiffHeader == nil {
			return nil
		}

		_, err := headers.StoreHeader(ctx, u.store, u.paths.MemfileHeader(), u.snap.MemfileDiffHeader)

		return err
	})

	eg.Go(func() error {
		if u.snap.RootfsDiffHeader == nil {
			return nil
		}

		_, err := headers.StoreHeader(ctx, u.store, u.paths.RootfsHeader(), u.snap.RootfsDiffHeader)

		return err
	})

	eg.Go(func() error {
		if memfilePath == "" {
			return nil
		}

		_, _, err := storage.UploadFramed(ctx, u.store, u.paths.Memfile(), storage.MemfileObjectType, memfilePath, storage.CompressConfig{})

		return err
	})

	eg.Go(func() error {
		if rootfsPath == "" {
			return nil
		}

		_, _, err := storage.UploadFramed(ctx, u.store, u.paths.Rootfs(), storage.RootFSObjectType, rootfsPath, storage.CompressConfig{})

		return err
	})

	eg.Go(func() error {
		return storage.UploadBlob(ctx, u.store, u.paths.Snapfile(), storage.SnapfileObjectType, u.snap.Snapfile.Path())
	})

	eg.Go(func() error {
		return storage.UploadBlob(ctx, u.store, u.paths.Metadata(), storage.MetadataObjectType, u.snap.Metafile.Path())
	})

	if err := eg.Wait(); err != nil {
		return nil, nil, err
	}

	if h := finalizeV3(u.snap.MemfileDiffHeader); h != nil {
		if err := u.publish(ctx, build.Memfile, h); err != nil {
			return nil, nil, err
		}
	}
	if h := finalizeV3(u.snap.RootfsDiffHeader); h != nil {
		if err := u.publish(ctx, build.Rootfs, h); err != nil {
			return nil, nil, err
		}
	}

	return nil, nil, nil
}

// finalizeV3 returns a shallow copy of src with IncompletePendingUpload cleared,
// or nil if src is nil. Safe shallow copy: only the bool field is mutated.
func finalizeV3(src *headers.Header) *headers.Header {
	if src == nil {
		return nil
	}
	h := *src
	h.IncompletePendingUpload = false

	return &h
}
