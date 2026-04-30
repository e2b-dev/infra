package sandbox

import (
	"context"
	"errors"
	"fmt"
	"maps"

	"github.com/google/uuid"
	"golang.org/x/sync/errgroup"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/build"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	headers "github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

type compressedUploader struct {
	persistence storage.StorageProvider
	snapshot    *Snapshot

	coord *UploadCoordinator

	memCfg    storage.CompressConfig
	rootfsCfg storage.CompressConfig
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

func (c *compressedUploader) upload(ctx context.Context) (memfileHeader, rootfsHeader []byte, err error) {
	memSrc, err := c.snapshot.MemfileDiff.CachePath()
	if err != nil {
		return nil, nil, fmt.Errorf("memfile diff path: %w", err)
	}

	rootfsSrc, err := c.snapshot.RootfsDiff.CachePath()
	if err != nil {
		return nil, nil, fmt.Errorf("rootfs diff path: %w", err)
	}

	eg, ctx := errgroup.WithContext(ctx)

	if c.snapshot.MemfileDiffHeader != nil {
		eg.Go(func() error {
			data, err := c.uploadFile(ctx, build.Memfile, memSrc, c.snapshot.MemfileDiffHeader, c.memCfg)
			memfileHeader = data

			return err
		})
	}

	if c.snapshot.RootfsDiffHeader != nil {
		eg.Go(func() error {
			data, err := c.uploadFile(ctx, build.Rootfs, rootfsSrc, c.snapshot.RootfsDiffHeader, c.rootfsCfg)
			rootfsHeader = data

			return err
		})
	}

	eg.Go(func() error {
		return storage.UploadBlob(ctx, c.persistence, c.snapshot.Paths.Snapfile(), storage.SnapfileObjectType, c.snapshot.Snapfile.Path())
	})

	eg.Go(func() error {
		return storage.UploadBlob(ctx, c.persistence, c.snapshot.Paths.Metadata(), storage.MetadataObjectType, c.snapshot.Metafile.Path())
	})

	if err := eg.Wait(); err != nil {
		return nil, nil, err
	}

	return memfileHeader, rootfsHeader, nil
}

func (c *compressedUploader) uploadFile(
	ctx context.Context,
	fileType build.DiffType,
	srcPath string,
	srcHeader *headers.Header,
	cfg storage.CompressConfig,
) ([]byte, error) {
	var selfBuild headers.BuildData

	if srcPath != "" {
		ft, checksum, err := storage.UploadFramed(ctx, c.persistence, c.snapshot.Paths.DataFile(string(fileType), cfg.CompressionType()), seekableTypeFor(fileType), srcPath, cfg)
		if err != nil {
			return nil, fmt.Errorf("%s upload: %w", fileType, err)
		}

		// Use the FrameTable's count — for sparse local files (e.g. memfile
		// diffs) this is the byte count actually streamed and checksummed by
		// UploadFramed, not the apparent file size from os.Stat.
		selfBuild = headers.BuildData{Size: ft.UncompressedSize(), Checksum: checksum}
		if ft.IsCompressed() {
			selfBuild.FrameData = ft
		}
	}

	h := srcHeader.CloneForUpload(headers.MetadataVersionV4)
	h.IncompletePendingUpload = false

	parentBuilds := srcHeader.Builds
	if c.snapshot.ParentBuildID != uuid.Nil {
		parentH, err := c.coord.WaitForFinalHeader(ctx, c.snapshot.ParentBuildID, fileType)
		if err != nil {
			return nil, fmt.Errorf("wait for parent %s/%s: %w", c.snapshot.ParentBuildID, fileType, err)
		}

		parentBuilds = parentH.Builds
	}

	// Self goes into the lineage even when srcPath == "" — empty diffs still
	// represent a layer that descendants must record as an ancestor.
	h.Builds = make(map[uuid.UUID]headers.BuildData, len(parentBuilds)+1)
	maps.Copy(h.Builds, parentBuilds)
	h.Builds[c.snapshot.BuildID] = selfBuild

	data, err := headers.StoreHeader(ctx, c.persistence, c.snapshot.Paths.HeaderFile(string(fileType)), h)
	if err != nil {
		return nil, fmt.Errorf("store %s header: %w", fileType, err)
	}

	dev, err := c.coord.FindInTemplateCache(ctx, c.snapshot.BuildID, fileType)
	if err == nil {
		dev.SwapHeader(h)
	} else if !errors.Is(err, ErrBuildNotInCache) {
		return nil, fmt.Errorf("load %s for swap: %w", fileType, err)
	}

	return data, nil
}
