package sandbox

import (
	"context"
	"fmt"
	"maps"

	"github.com/google/uuid"
	"golang.org/x/sync/errgroup"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/build"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	headers "github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

func (u *Upload) runV4(ctx context.Context) error {
	memSrc, err := u.snap.MemfileDiff.CachePath()
	if err != nil {
		return fmt.Errorf("memfile diff path: %w", err)
	}

	rootfsSrc, err := u.snap.RootfsDiff.CachePath()
	if err != nil {
		return fmt.Errorf("rootfs diff path: %w", err)
	}

	eg, ctx := errgroup.WithContext(ctx)

	if u.snap.MemfileDiffHeader != nil {
		eg.Go(func() error {
			return u.uploadFramed(ctx, build.Memfile, memSrc, u.snap.MemfileDiffHeader, u.mem)
		})
	}

	if u.snap.RootfsDiffHeader != nil {
		eg.Go(func() error {
			return u.uploadFramed(ctx, build.Rootfs, rootfsSrc, u.snap.RootfsDiffHeader, u.root)
		})
	}

	eg.Go(func() error {
		return storage.UploadBlob(ctx, u.store, u.paths.Snapfile(), storage.SnapfileObjectType, u.snap.Snapfile.Path())
	})

	eg.Go(func() error {
		return storage.UploadBlob(ctx, u.store, u.paths.Metadata(), storage.MetadataObjectType, u.snap.Metafile.Path())
	})

	return eg.Wait()
}

func (u *Upload) uploadFramed(
	ctx context.Context,
	fileType build.DiffType,
	srcPath string,
	srcHeader *headers.Header,
	cfg storage.CompressConfig,
) error {
	var selfBuild headers.BuildData

	if srcPath != "" {
		ft, checksum, err := storage.UploadFramed(ctx, u.store, u.paths.DataFile(string(fileType), cfg.CompressionType()), seekableTypeFor(fileType), srcPath, cfg)
		if err != nil {
			return fmt.Errorf("%s upload: %w", fileType, err)
		}

		// FrameTable count, not os.Stat: sparse memfile diffs stream less than
		// they appear on disk.
		selfBuild = headers.BuildData{Size: ft.UncompressedSize(), Checksum: checksum}
		if ft.IsCompressed() {
			selfBuild.FrameData = ft
		}
	}

	h := srcHeader.CloneForUpload(headers.MetadataVersionV4)
	h.IncompletePendingUpload = false

	builds := srcHeader.Builds
	if u.parent != uuid.Nil && u.uploads != nil {
		parentHeader, err := u.uploads.Wait(ctx, u.parent, fileType)
		if err != nil {
			return fmt.Errorf("wait for parent %s/%s: %w", u.parent, fileType, err)
		}

		builds = parentHeader.Builds
	}

	// Empty diffs still represent a layer descendants must record as an ancestor.
	h.Builds = make(map[uuid.UUID]headers.BuildData, len(builds)+1)
	maps.Copy(h.Builds, builds)
	h.Builds[u.buildID] = selfBuild

	if err := headers.StoreHeader(ctx, u.store, u.paths.HeaderFile(string(fileType)), h); err != nil {
		return fmt.Errorf("store %s header: %w", fileType, err)
	}

	return u.publish(ctx, fileType, h)
}

func seekableTypeFor(fileType build.DiffType) storage.SeekableObjectType {
	switch fileType {
	case build.Memfile:
		return storage.MemfileObjectType
	case build.Rootfs:
		return storage.RootFSObjectType
	}

	return storage.UnknownSeekableObjectType
}
