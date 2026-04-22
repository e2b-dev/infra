package sandbox

import (
	"context"
	"fmt"
	"os"

	"github.com/google/uuid"
	"golang.org/x/sync/errgroup"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	headers "github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

type compressedUploader struct {
	buildUploader

	memCompressConfig    storage.CompressConfig
	rootfsCompressConfig storage.CompressConfig
}

func (c *compressedUploader) Upload(ctx context.Context) (memfileHeader, rootfsHeader []byte, err error) {
	memfileLocalPath, err := c.snapshot.MemfileDiff.CachePath()
	if err != nil {
		return nil, nil, fmt.Errorf("error getting memfile diff path: %w", err)
	}

	rootfsLocalPath, err := c.snapshot.RootfsDiff.CachePath()
	if err != nil {
		return nil, nil, fmt.Errorf("error getting rootfs diff path: %w", err)
	}

	eg, ctx := errgroup.WithContext(ctx)

	eg.Go(func() error {
		data, err := c.uploadFile(ctx, memfileLocalPath, c.memCompressConfig, storage.MemfileObjectType,
			c.snapshot.MemfileDiffHeader,
			c.paths.MemfileCompressed(c.memCompressConfig.CompressionType()),
			c.paths.MemfileHeader())
		if err != nil {
			return fmt.Errorf("memfile upload: %w", err)
		}
		memfileHeader = data

		return nil
	})

	eg.Go(func() error {
		data, err := c.uploadFile(ctx, rootfsLocalPath, c.rootfsCompressConfig, storage.RootFSObjectType,
			c.snapshot.RootfsDiffHeader,
			c.paths.RootfsCompressed(c.rootfsCompressConfig.CompressionType()),
			c.paths.RootfsHeader())
		if err != nil {
			return fmt.Errorf("rootfs upload: %w", err)
		}
		rootfsHeader = data

		return nil
	})

	eg.Go(func() error {
		return storage.UploadBlob(ctx, c.persistence, c.paths.Snapfile(), storage.SnapfileObjectType, c.snapshot.Snapfile.Path())
	})

	eg.Go(func() error {
		return storage.UploadBlob(ctx, c.persistence, c.paths.Metadata(), storage.MetadataObjectType, c.snapshot.Metafile.Path())
	})

	if err := eg.Wait(); err != nil {
		return nil, nil, err
	}

	return memfileHeader, rootfsHeader, nil
}

// uploadFile uploads data (if any), finalizes h with the self-entry, then
// serializes and PUTs the V4 header. The deferred CancelOnError cancels h
// if uploadFile returns with an error.
func (c *compressedUploader) uploadFile(
	ctx context.Context,
	localPath string,
	cfg storage.CompressConfig,
	objType storage.SeekableObjectType,
	h *headers.Header,
	dataPath string,
	headerPath string,
) (_ []byte, e error) {
	defer func() { h.CancelOnError(e) }()

	if localPath != "" {
		dep, err := c.uploadData(ctx, localPath, cfg, dataPath, objType)
		if err != nil {
			return nil, err
		}
		h.FinalizeDependencies(map[uuid.UUID]headers.Dependency{h.Metadata.BuildId: dep}, nil)
	} else {
		// Header-only layer: no self data to upload. Unblock waiters so
		// cached references to h don't hang on dependenciesReady.
		h.FinalizeDependencies(nil, nil)
	}

	// A header-only layer may exist.
	headerBytes, err := h.SerializeForV4Upload()
	if err != nil {
		return nil, fmt.Errorf("serialize header: %w", err)
	}

	blob, err := c.persistence.OpenBlob(ctx, headerPath, storage.MetadataObjectType)
	if err != nil {
		return nil, fmt.Errorf("open header blob %s: %w", headerPath, err)
	}

	if err := blob.Put(ctx, headerBytes); err != nil {
		return nil, fmt.Errorf("put header blob %s: %w", headerPath, err)
	}

	return headerBytes, nil
}

func (c *compressedUploader) uploadData(
	ctx context.Context,
	localPath string,
	cfg storage.CompressConfig,
	dataPath string,
	objType storage.SeekableObjectType,
) (headers.Dependency, error) {
	if cfg.IsCompressionEnabled() {
		ft, checksum, err := storage.UploadFramed(ctx, c.persistence, dataPath, objType, localPath, cfg)
		if err != nil {
			return headers.Dependency{}, fmt.Errorf("compressed data upload: %w", err)
		}

		return headers.Dependency{Size: ft.UncompressedSize(), Checksum: checksum, FrameData: ft}, nil
	}

	// Stat before the upload so an eviction mid-upload doesn't mask a
	// successful remote write as a header-finalization error.
	fi, err := os.Stat(localPath)
	if err != nil {
		return headers.Dependency{}, fmt.Errorf("stat %s: %w", localPath, err)
	}

	_, checksum, err := storage.UploadFramed(ctx, c.persistence, dataPath, objType, localPath, storage.CompressConfig{})
	if err != nil {
		return headers.Dependency{}, fmt.Errorf("uncompressed data upload: %w", err)
	}

	return headers.Dependency{Size: fi.Size(), Checksum: checksum}, nil
}

var _ BuildUploader = (*compressedUploader)(nil)
