package sandbox

import (
	"context"
	"fmt"
	"io"
	"os"

	"golang.org/x/sync/errgroup"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/build"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	headers "github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

type TemplateBuild struct {
	paths       storage.Paths
	persistence storage.StorageProvider

	memfileHeader *headers.Header
	rootfsHeader  *headers.Header
}

func NewTemplateBuild(memfileHeader *headers.Header, rootfsHeader *headers.Header, persistence storage.StorageProvider, paths storage.Paths) *TemplateBuild {
	return &TemplateBuild{
		persistence: persistence,
		paths:       paths,

		memfileHeader: memfileHeader,
		rootfsHeader:  rootfsHeader,
	}
}

func (t *TemplateBuild) Remove(ctx context.Context) error {
	err := t.persistence.DeleteObjectsWithPrefix(ctx, t.paths.StorageDir())
	if err != nil {
		return fmt.Errorf("error when removing template build '%s': %w", t.paths.StorageDir(), err)
	}

	return nil
}

func (t *TemplateBuild) uploadMemfileHeader(ctx context.Context, h *headers.Header) error {
	object, err := t.persistence.OpenBlob(ctx, t.paths.MemfileHeader(), storage.MemfileHeaderObjectType)
	if err != nil {
		return err
	}

	serialized, err := headers.Serialize(h.Metadata, h.Mapping)
	if err != nil {
		return fmt.Errorf("error when serializing memfile header: %w", err)
	}

	err = object.Put(ctx, serialized)
	if err != nil {
		return fmt.Errorf("error when uploading memfile header: %w", err)
	}

	return nil
}

func (t *TemplateBuild) uploadDiff(ctx context.Context, diff build.Diff, path string, objectType storage.SeekableObjectType) error {
	uploaded, err := t.tryZeroCopyUpload(ctx, diff, path, objectType)
	if err != nil {
		return err
	}

	if uploaded {
		return nil
	}

	// Fallback: upload from the file on disk.
	cachePath, err := diff.CachePath()
	if err != nil {
		return err
	}

	object, err := t.persistence.OpenSeekable(ctx, path, objectType)
	if err != nil {
		return err
	}

	return object.StoreFile(ctx, cachePath)
}

// tryZeroCopyUpload attempts to upload diff data directly from mmap.
// Returns (true, nil) on success, (false, nil) when the object doesn't
// support DataStorer or the diff has no mmap data, or (false, err) on error.
// The mmap RLock is acquired and released within this function, so callers
// can safely proceed with file-based I/O after it returns.
func (t *TemplateBuild) tryZeroCopyUpload(ctx context.Context, diff build.Diff, path string, objectType storage.SeekableObjectType) (bool, error) {
	data, release, err := diff.Data()
	if err != nil {
		release()

		return false, err
	}
	defer release()

	if data == nil {
		return false, nil
	}

	object, err := t.persistence.OpenSeekable(ctx, path, objectType)
	if err != nil {
		return false, err
	}

	if ds, ok := object.(storage.DataStorer); ok {
		return true, ds.StoreData(ctx, data)
	}

	return false, nil
}

func (t *TemplateBuild) uploadRootfsHeader(ctx context.Context, h *headers.Header) error {
	object, err := t.persistence.OpenBlob(ctx, t.paths.RootfsHeader(), storage.RootFSHeaderObjectType)
	if err != nil {
		return err
	}

	serialized, err := headers.Serialize(h.Metadata, h.Mapping)
	if err != nil {
		return fmt.Errorf("error when serializing memfile header: %w", err)
	}

	err = object.Put(ctx, serialized)
	if err != nil {
		return fmt.Errorf("error when uploading memfile header: %w", err)
	}

	return nil
}

// Snap-file is small enough so we don't use composite upload.
func (t *TemplateBuild) uploadSnapfile(ctx context.Context, path string) error {
	object, err := t.persistence.OpenBlob(ctx, t.paths.Snapfile(), storage.SnapfileObjectType)
	if err != nil {
		return err
	}

	if err = uploadFileAsBlob(ctx, object, path); err != nil {
		return fmt.Errorf("error when uploading snapfile: %w", err)
	}

	return nil
}

// Metadata is small enough so we don't use composite upload.
func (t *TemplateBuild) uploadMetadata(ctx context.Context, path string) error {
	object, err := t.persistence.OpenBlob(ctx, t.paths.Metadata(), storage.MetadataObjectType)
	if err != nil {
		return err
	}

	if err := uploadFileAsBlob(ctx, object, path); err != nil {
		return fmt.Errorf("error when uploading metadata: %w", err)
	}

	return nil
}

func uploadFileAsBlob(ctx context.Context, b storage.Blob, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("failed to open file %s: %w", path, err)
	}
	defer f.Close()

	data, err := io.ReadAll(f)
	if err != nil {
		return fmt.Errorf("failed to read file %s: %w", path, err)
	}

	err = b.Put(ctx, data)
	if err != nil {
		return fmt.Errorf("failed to write data to object: %w", err)
	}

	return nil
}

func (t *TemplateBuild) Upload(ctx context.Context, metadataPath string, fcSnapfilePath string, memfileDiff build.Diff, rootfsDiff build.Diff) error {
	eg, ctx := errgroup.WithContext(ctx)

	eg.Go(func() error {
		if t.memfileHeader == nil {
			return nil
		}

		return t.uploadMemfileHeader(ctx, t.memfileHeader)
	})

	eg.Go(func() error {
		if t.rootfsHeader == nil {
			return nil
		}

		return t.uploadRootfsHeader(ctx, t.rootfsHeader)
	})

	eg.Go(func() error {
		return t.uploadDiff(ctx, rootfsDiff, t.paths.Rootfs(), storage.RootFSObjectType)
	})

	eg.Go(func() error {
		return t.uploadDiff(ctx, memfileDiff, t.paths.Memfile(), storage.MemfileObjectType)
	})

	eg.Go(func() error {
		return t.uploadSnapfile(ctx, fcSnapfilePath)
	})

	eg.Go(func() error {
		return t.uploadMetadata(ctx, metadataPath)
	})

	return eg.Wait()
}
