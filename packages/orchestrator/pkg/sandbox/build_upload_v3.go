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
	memfilePath, err := u.snap.MemorySnapshot.Diff.CachePath(ctx)
	if err != nil {
		return fmt.Errorf("error getting memfile diff path: %w", err)
	}

	rootfsPath, err := u.snap.RootfsDiff.CachePath(ctx)
	if err != nil {
		return fmt.Errorf("error getting rootfs diff path: %w", err)
	}

	eg, egCtx := errgroup.WithContext(ctx)

	eg.Go(func() error {
		h, err := u.snap.MemorySnapshot.DiffHeader.WaitWithContext(egCtx)
		if err != nil {
			return fmt.Errorf("wait memfile diff header: %w", err)
		}
		if h == nil {
			return nil
		}

		return storeHeaderWithMetrics(egCtx, u.store, u.paths.MemfileHeader(), uploadFileMemfileHeader, finalizeV3(h), storage.WithMetadata(u.objectMetadata))
	})

	eg.Go(func() error {
		h, err := u.snap.RootfsDiffHeader.WaitWithContext(egCtx)
		if err != nil {
			return fmt.Errorf("wait rootfs diff header: %w", err)
		}
		if h == nil {
			return nil
		}

		return storeHeaderWithMetrics(egCtx, u.store, u.paths.RootfsHeader(), uploadFileRootfsHeader, finalizeV3(h), storage.WithMetadata(u.objectMetadata))
	})

	meta := storage.WithMetadata(u.objectMetadata)

	eg.Go(func() error {
		if memfilePath == "" {
			return nil
		}

		h, err := u.snap.MemorySnapshot.DiffHeader.WaitWithContext(egCtx)
		if err != nil {
			return fmt.Errorf("wait memfile diff header: %w", err)
		}

		info, err := os.Stat(memfilePath)
		if err != nil {
			return fmt.Errorf("memfile stat: %w", err)
		}
		_, _, err = storage.UploadFramed(egCtx, u.store, u.paths.Memfile(), memfilePath, storage.WithMetadata(u.layerSizeMetadata(h)))
		if err != nil {
			return err
		}
		recordUploadCompression(egCtx, uploadFileMemfile, storage.CompressConfig{}, info.Size(), info.Size())

		return nil
	})

	eg.Go(func() error {
		if rootfsPath == "" {
			return nil
		}

		h, err := u.snap.RootfsDiffHeader.WaitWithContext(egCtx)
		if err != nil {
			return fmt.Errorf("wait rootfs diff header: %w", err)
		}

		info, err := os.Stat(rootfsPath)
		if err != nil {
			return fmt.Errorf("rootfs stat: %w", err)
		}
		_, _, err = storage.UploadFramed(egCtx, u.store, u.paths.Rootfs(), rootfsPath, storage.WithMetadata(u.layerSizeMetadata(h)))
		if err != nil {
			return err
		}
		recordUploadCompression(egCtx, uploadFileRootfs, storage.CompressConfig{}, info.Size(), info.Size())

		return nil
	})

	eg.Go(func() error {
		// Filesystem-only snapshots resume by reboot, not snapfile restore, so
		// the snapfile (created only for its disk-flush side effect) is not uploaded.
		if u.snap.FilesystemSnapshot {
			return nil
		}

		return uploadBlobWithMetrics(egCtx, u.store, u.paths.Snapfile(), u.snap.Snapfile.Path(), uploadFileSnap, meta)
	})

	eg.Go(func() error {
		return uploadBlobWithMetrics(egCtx, u.store, u.paths.Metadata(), u.snap.Metafile.Path(), uploadFileMeta, meta)
	})

	if err := eg.Wait(); err != nil {
		return err
	}

	// Body uploads done; headers must be ready by now (the per-file Goroutines
	// above already Wait-ed). Wait() is a fast lookup here.
	memfileDiffHeader, err := u.snap.MemorySnapshot.DiffHeader.WaitWithContext(ctx)
	if err != nil {
		return fmt.Errorf("wait memfile diff header: %w", err)
	}
	rootfsDiffHeader, err := u.snap.RootfsDiffHeader.WaitWithContext(ctx)
	if err != nil {
		return fmt.Errorf("wait rootfs diff header: %w", err)
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
