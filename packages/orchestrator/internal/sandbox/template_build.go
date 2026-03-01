package sandbox

import (
	"context"
	"fmt"
	"io"
	"os"

	"golang.org/x/sync/errgroup"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/build"
	featureflags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	headers "github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

type TemplateBuild struct {
	files       storage.TemplateFiles
	persistence storage.StorageProvider
	ff          *featureflags.Client

	snapshot *Snapshot
	pending  *PendingBuildInfo
}

func NewTemplateBuild(snapshot *Snapshot, persistence storage.StorageProvider, files storage.TemplateFiles, ff *featureflags.Client, pending *PendingBuildInfo) *TemplateBuild {
	if pending == nil {
		pending = &PendingBuildInfo{}
	}

	return &TemplateBuild{
		persistence: persistence,
		files:       files,
		ff:          ff,
		snapshot:    snapshot,
		pending:     pending,
	}
}

func (t *TemplateBuild) Remove(ctx context.Context) error {
	err := t.persistence.DeleteObjectsWithPrefix(ctx, t.files.StorageDir())
	if err != nil {
		return fmt.Errorf("error when removing template build '%s': %w", t.files.StorageDir(), err)
	}

	return nil
}

// diffPath returns the cache path for a diff, or nil if the diff is NoDiff.
func diffPath(d build.Diff) (*string, error) {
	if _, ok := d.(*build.NoDiff); ok {
		return nil, nil
	}

	p, err := d.CachePath()
	if err != nil {
		return nil, err
	}

	return &p, nil
}

func (t *TemplateBuild) uploadMemfile(ctx context.Context, memfilePath string) error {
	object, err := t.persistence.OpenFramedFile(ctx, t.files.StorageMemfilePath())
	if err != nil {
		return err
	}

	if _, _, err := object.StoreFile(ctx, memfilePath, nil); err != nil {
		return fmt.Errorf("error when uploading memfile: %w", err)
	}

	return nil
}

func (t *TemplateBuild) uploadRootfs(ctx context.Context, rootfsPath string) error {
	object, err := t.persistence.OpenFramedFile(ctx, t.files.StorageRootfsPath())
	if err != nil {
		return err
	}

	if _, _, err := object.StoreFile(ctx, rootfsPath, nil); err != nil {
		return fmt.Errorf("error when uploading rootfs: %w", err)
	}

	return nil
}

// Snap-file is small enough so we don't use composite upload.
func (t *TemplateBuild) uploadSnapfile(ctx context.Context, path string) error {
	object, err := t.persistence.OpenBlob(ctx, t.files.StorageSnapfilePath())
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
	object, err := t.persistence.OpenBlob(ctx, t.files.StorageMetadataPath())
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

// UploadExceptV4Headers uploads all template build files except compressed (V4) headers.
// The compress-config feature flag exclusively controls the format:
//   - Compressed: uploads only compressed data (no V3 headers, no uncompressed data)
//   - Uncompressed: uploads V3 headers + uncompressed data only
//
// Snapfile and metadata are always uploaded.
// Frame tables from compressed uploads are registered in the shared PendingBuildInfo
// for later use by UploadV4Header.
// Returns true if compression was enabled (i.e. V4 headers need uploading).
func (t *TemplateBuild) UploadExceptV4Headers(ctx context.Context) (hasCompressed bool, err error) {
	memfilePath, err := diffPath(t.snapshot.MemfileDiff)
	if err != nil {
		return false, fmt.Errorf("error getting memfile diff path: %w", err)
	}

	rootfsPath, err := diffPath(t.snapshot.RootfsDiff)
	if err != nil {
		return false, fmt.Errorf("error getting rootfs diff path: %w", err)
	}

	compressOpts := storage.GetUploadOptions(ctx, t.ff)
	eg, ctx := errgroup.WithContext(ctx)
	buildID := t.files.BuildID

	if compressOpts != nil {
		// COMPRESSED: upload only compressed data (no V3 headers, no uncompressed data)
		if memfilePath != nil {
			hasCompressed = true

			eg.Go(func() error {
				ft, checksum, err := t.uploadCompressedFile(ctx, *memfilePath, storage.MemfileName, compressOpts)
				if err != nil {
					return fmt.Errorf("compressed memfile upload: %w", err)
				}

				uncompressedSize, _ := ft.Size()
				t.pending.add(pendingBuildInfoKey(buildID, storage.MemfileName), ft, uncompressedSize, checksum)

				return nil
			})
		}

		if rootfsPath != nil {
			hasCompressed = true

			eg.Go(func() error {
				ft, checksum, err := t.uploadCompressedFile(ctx, *rootfsPath, storage.RootfsName, compressOpts)
				if err != nil {
					return fmt.Errorf("compressed rootfs upload: %w", err)
				}

				uncompressedSize, _ := ft.Size()
				t.pending.add(pendingBuildInfoKey(buildID, storage.RootfsName), ft, uncompressedSize, checksum)

				return nil
			})
		}
	} else {
		// UNCOMPRESSED: upload V3 headers + uncompressed data only
		eg.Go(func() error {
			if t.snapshot.MemfileDiffHeader == nil {
				return nil
			}

			return headers.StoreHeader(ctx, t.persistence, t.files.HeaderPath(storage.MemfileName), t.snapshot.MemfileDiffHeader)
		})

		eg.Go(func() error {
			if t.snapshot.RootfsDiffHeader == nil {
				return nil
			}

			return headers.StoreHeader(ctx, t.persistence, t.files.HeaderPath(storage.RootfsName), t.snapshot.RootfsDiffHeader)
		})

		eg.Go(func() error {
			if memfilePath == nil {
				return nil
			}

			return t.uploadMemfile(ctx, *memfilePath)
		})

		eg.Go(func() error {
			if rootfsPath == nil {
				return nil
			}

			return t.uploadRootfs(ctx, *rootfsPath)
		})
	}

	// Snapfile + metadata (always)
	eg.Go(func() error {
		return t.uploadSnapfile(ctx, t.snapshot.Snapfile.Path())
	})

	eg.Go(func() error {
		return t.uploadMetadata(ctx, t.snapshot.Metafile.Path())
	})

	if err := eg.Wait(); err != nil {
		return false, err
	}

	return hasCompressed, nil
}

// uploadCompressedFile compresses and uploads a file to the compressed data path.
func (t *TemplateBuild) uploadCompressedFile(ctx context.Context, localPath, fileName string, opts *storage.FramedUploadOptions) (*storage.FrameTable, [32]byte, error) {
	objectPath := t.files.CompressedDataPath(fileName, opts.CompressionType)

	object, err := t.persistence.OpenFramedFile(ctx, objectPath)
	if err != nil {
		return nil, [32]byte{}, fmt.Errorf("error opening framed file for %s: %w", objectPath, err)
	}

	ft, checksum, err := object.StoreFile(ctx, localPath, opts)
	if err != nil {
		return nil, [32]byte{}, fmt.Errorf("error compressing %s to %s: %w", fileName, objectPath, err)
	}

	return ft, checksum, nil
}

// UploadV4Header applies pending frame tables to headers and uploads them as V4 compressed format.
// Frame tables must have been registered by a prior UploadExceptV4Headers call.
func (t *TemplateBuild) UploadV4Header(ctx context.Context) error {
	eg, ctx := errgroup.WithContext(ctx)

	if t.snapshot.MemfileDiffHeader != nil {
		eg.Go(func() error {
			if err := t.pending.applyToHeader(t.snapshot.MemfileDiffHeader, storage.MemfileName); err != nil {
				return fmt.Errorf("apply frames to memfile header: %w", err)
			}

			t.snapshot.MemfileDiffHeader.Metadata.Version = headers.MetadataVersionCompressed

			return headers.StoreHeader(ctx, t.persistence, t.files.HeaderPath(storage.MemfileName), t.snapshot.MemfileDiffHeader)
		})
	}

	if t.snapshot.RootfsDiffHeader != nil {
		eg.Go(func() error {
			if err := t.pending.applyToHeader(t.snapshot.RootfsDiffHeader, storage.RootfsName); err != nil {
				return fmt.Errorf("apply frames to rootfs header: %w", err)
			}

			t.snapshot.RootfsDiffHeader.Metadata.Version = headers.MetadataVersionCompressed

			return headers.StoreHeader(ctx, t.persistence, t.files.HeaderPath(storage.RootfsName), t.snapshot.RootfsDiffHeader)
		})
	}

	return eg.Wait()
}

// UploadAtOnce uploads all template build files including V4 headers for a single-layer build.
// For multi-layer builds, use UploadExceptV4Headers + UploadV4Header with a shared
// PendingBuildInfo instead.
func (t *TemplateBuild) UploadAtOnce(ctx context.Context) error {
	hasCompressed, err := t.UploadExceptV4Headers(ctx)
	if err != nil {
		return err
	}

	if hasCompressed {
		if err := t.UploadV4Header(ctx); err != nil {
			return fmt.Errorf("error uploading compressed headers: %w", err)
		}
	}

	return nil
}
