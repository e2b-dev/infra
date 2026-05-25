//go:build linux

package sandbox

import (
	"context"
	"fmt"
	"os"

	"golang.org/x/sync/errgroup"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/build"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	headers "github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

func (u *Upload) runV3(ctx context.Context) error {
	memfilePath, err := u.snap.MemfileDiff.CachePath(ctx)
	if err != nil {
		return fmt.Errorf("error getting memfile diff path: %w", err)
	}

	rootfsPath, err := u.snap.RootfsDiff.CachePath(ctx)
	if err != nil {
		return fmt.Errorf("error getting rootfs diff path: %w", err)
	}

	memfileDiffHeader, err := u.snap.MemfileDiffHeader.WaitWithContext(ctx)
	if err != nil {
		return fmt.Errorf("wait memfile diff header: %w", err)
	}
	rootfsDiffHeader, err := u.snap.RootfsDiffHeader.WaitWithContext(ctx)
	if err != nil {
		return fmt.Errorf("wait rootfs diff header: %w", err)
	}

	eg, egCtx := errgroup.WithContext(ctx)

	eg.Go(func() error {
		if memfileDiffHeader == nil {
			return nil
		}

		return storeHeaderWithMetrics(egCtx, u.store, u.paths.MemfileHeader(), string(build.Memfile), finalizeV3(memfileDiffHeader))
	})

	eg.Go(func() error {
		if rootfsDiffHeader == nil {
			return nil
		}

		return storeHeaderWithMetrics(egCtx, u.store, u.paths.RootfsHeader(), string(build.Rootfs), finalizeV3(rootfsDiffHeader))
	})

	meta := storage.WithMetadata(u.objectMetadata)

	eg.Go(func() error {
		if memfilePath == "" {
			return nil
		}

		info, err := os.Stat(memfilePath)
		if err != nil {
			return fmt.Errorf("memfile stat: %w", err)
		}
		_, _, err = storage.UploadFramed(egCtx, u.store, u.paths.Memfile(), storage.MemfileObjectType, memfilePath, meta)
		if err != nil {
			return err
		}
		recordUploadCompression(egCtx, uploadArtifactData, string(build.Memfile), storage.CompressConfig{}, info.Size(), info.Size())

		return nil
	})

	eg.Go(func() error {
		if rootfsPath == "" {
			return nil
		}

		info, err := os.Stat(rootfsPath)
		if err != nil {
			return fmt.Errorf("rootfs stat: %w", err)
		}
		_, _, err = storage.UploadFramed(egCtx, u.store, u.paths.Rootfs(), storage.RootFSObjectType, rootfsPath, meta)
		if err != nil {
			return err
		}
		recordUploadCompression(egCtx, uploadArtifactData, string(build.Rootfs), storage.CompressConfig{}, info.Size(), info.Size())

		return nil
	})

	eg.Go(func() error {
		return storage.UploadBlob(egCtx, u.store, u.paths.Snapfile(), storage.SnapfileObjectType, u.snap.Snapfile.Path(), meta)
	})

	eg.Go(func() error {
		return storage.UploadBlob(egCtx, u.store, u.paths.Metadata(), storage.MetadataObjectType, u.snap.Metafile.Path(), meta)
	})

	if err := eg.Wait(); err != nil {
		return err
	}

	if memfileDiffHeader != nil {
		if err := u.appendAncestorBuilds(ctx, nil, memfileDiffHeader.Mapping, build.Memfile); err != nil {
			return err
		}
	}
	if h := finalizeV3(memfileDiffHeader); h != nil {
		if err := u.publish(ctx, build.Memfile, h); err != nil {
			return err
		}
	}

	if rootfsDiffHeader != nil {
		if err := u.appendAncestorBuilds(ctx, nil, rootfsDiffHeader.Mapping, build.Rootfs); err != nil {
			return err
		}
	}
	if h := finalizeV3(rootfsDiffHeader); h != nil {
		if err := u.publish(ctx, build.Rootfs, h); err != nil {
			return err
		}
	}

	return nil
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
