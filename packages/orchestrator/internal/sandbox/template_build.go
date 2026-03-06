package sandbox

import (
	"context"
	"fmt"
	"io"
	"os"

	"golang.org/x/sync/errgroup"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/build"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	headers "github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

type TemplateBuild struct {
	files       storage.TemplateFiles
	persistence storage.StorageProvider

	snapshot *Snapshot
	pending  *PendingBuildInfo

	// Track which file types were uploaded compressed,
	// so UploadV4Header knows which headers to finalize.
	memfileCompressed bool
	rootfsCompressed  bool
}

func NewTemplateBuild(snapshot *Snapshot, persistence storage.StorageProvider, files storage.TemplateFiles, pending *PendingBuildInfo) *TemplateBuild {
	if pending == nil {
		pending = &PendingBuildInfo{}
	}

	return &TemplateBuild{
		persistence: persistence,
		files:       files,
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

// uploadUncompressedFile uploads a single data file without compression.
func (t *TemplateBuild) uploadUncompressedFile(ctx context.Context, localPath, fileName string) error {
	object, err := t.persistence.OpenFramedFile(ctx, t.files.DataPath(fileName))
	if err != nil {
		return err
	}

	if _, _, err := object.StoreFile(ctx, localPath, nil, nil); err != nil {
		return fmt.Errorf("error when uploading %s: %w", fileName, err)
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

// scheduleFileUpload schedules the upload of a single data file (memfile or rootfs).
// If cfg is non-nil, the file is compressed; otherwise it uploads uncompressed with a V3 header.
func (t *TemplateBuild) scheduleFileUpload(
	eg *errgroup.Group,
	ctx context.Context,
	localPath *string,
	fileName string,
	diffHeader *headers.Header,
	cfg *storage.CompressConfig,
	compressed *bool,
) {
	if cfg != nil {
		// COMPRESSED: upload only compressed data
		if localPath != nil {
			*compressed = true

			eg.Go(func() error {
				ft, checksum, err := t.uploadCompressedFile(ctx, *localPath, fileName, cfg)
				if err != nil {
					return fmt.Errorf("compressed %s upload: %w", fileName, err)
				}

				uncompressedSize, _ := ft.Size()
				t.pending.add(pendingBuildInfoKey(t.files.BuildID, fileName), ft, uncompressedSize, checksum)

				return nil
			})
		}
	} else {
		// UNCOMPRESSED: upload V3 header + uncompressed data
		eg.Go(func() error {
			if diffHeader == nil {
				return nil
			}

			return headers.StoreHeader(ctx, t.persistence, t.files.HeaderPath(fileName), diffHeader)
		})

		eg.Go(func() error {
			if localPath == nil {
				return nil
			}

			return t.uploadUncompressedFile(ctx, *localPath, fileName)
		})
	}
}

// UploadExceptV4Headers uploads all template build files except compressed (V4) headers.
// memfileOpts and rootfsOpts independently control compression per file type:
//   - non-nil opts: uploads only compressed data (no V3 header, no uncompressed data)
//   - nil opts: uploads V3 header + uncompressed data only
//
// Snapfile and metadata are always uploaded.
// Frame tables from compressed uploads are registered in the shared PendingBuildInfo
// for later use by UploadV4Header.
// Returns true if any file was compressed (i.e. V4 headers need uploading).
func (t *TemplateBuild) UploadExceptV4Headers(ctx context.Context, memfileCfg, rootfsCfg *storage.CompressConfig) (hasCompressed bool, err error) {
	memfilePath, err := diffPath(t.snapshot.MemfileDiff)
	if err != nil {
		return false, fmt.Errorf("error getting memfile diff path: %w", err)
	}

	rootfsPath, err := diffPath(t.snapshot.RootfsDiff)
	if err != nil {
		return false, fmt.Errorf("error getting rootfs diff path: %w", err)
	}

	eg, ctx := errgroup.WithContext(ctx)

	t.scheduleFileUpload(eg, ctx, memfilePath, storage.MemfileName, t.snapshot.MemfileDiffHeader, memfileCfg, &t.memfileCompressed)
	t.scheduleFileUpload(eg, ctx, rootfsPath, storage.RootfsName, t.snapshot.RootfsDiffHeader, rootfsCfg, &t.rootfsCompressed)

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

	return t.memfileCompressed || t.rootfsCompressed, nil
}

// uploadCompressedFile compresses and uploads a file to the compressed data path.
func (t *TemplateBuild) uploadCompressedFile(ctx context.Context, localPath, fileName string, cfg *storage.CompressConfig) (*storage.FrameTable, [32]byte, error) {
	objectPath := t.files.CompressedDataPath(fileName, cfg.CompressionType())

	object, err := t.persistence.OpenFramedFile(ctx, objectPath)
	if err != nil {
		return nil, [32]byte{}, fmt.Errorf("error opening framed file for %s: %w", objectPath, err)
	}

	ft, checksum, err := object.StoreFile(ctx, localPath, cfg, nil)
	if err != nil {
		return nil, [32]byte{}, fmt.Errorf("error compressing %s to %s: %w", fileName, objectPath, err)
	}

	return ft, checksum, nil
}

// UploadV4Header applies pending frame tables to headers and uploads them as V4 compressed format.
// Frame tables must have been registered by a prior UploadExceptV4Headers call.
// Only files that were uploaded compressed (tracked in compressedFiles) get V4 headers.
//
// The snapshot headers are cloned before mutation because the originals may be
// concurrently read by sandboxes resumed from the template cache (e.g. the
// optimize phase's UFFD handlers).
func (t *TemplateBuild) UploadV4Header(ctx context.Context) error {
	eg, ctx := errgroup.WithContext(ctx)

	if t.snapshot.MemfileDiffHeader != nil && t.memfileCompressed {
		eg.Go(func() error {
			h := t.snapshot.MemfileDiffHeader.CloneForUpload()

			if err := t.pending.applyToHeader(h, storage.MemfileName); err != nil {
				return fmt.Errorf("apply frames to memfile header: %w", err)
			}

			h.Metadata.Version = headers.MetadataVersionCompressed

			return headers.StoreHeader(ctx, t.persistence, t.files.HeaderPath(storage.MemfileName), h)
		})
	}

	if t.snapshot.RootfsDiffHeader != nil && t.rootfsCompressed {
		eg.Go(func() error {
			h := t.snapshot.RootfsDiffHeader.CloneForUpload()

			if err := t.pending.applyToHeader(h, storage.RootfsName); err != nil {
				return fmt.Errorf("apply frames to rootfs header: %w", err)
			}

			h.Metadata.Version = headers.MetadataVersionCompressed

			return headers.StoreHeader(ctx, t.persistence, t.files.HeaderPath(storage.RootfsName), h)
		})
	}

	return eg.Wait()
}

// UploadAtOnce uploads all template build files including V4 headers for a single-layer build.
// For multi-layer builds, use UploadExceptV4Headers + UploadV4Header with a shared
// PendingBuildInfo instead.
func (t *TemplateBuild) UploadAtOnce(ctx context.Context, memfileCfg, rootfsCfg *storage.CompressConfig) error {
	hasCompressed, err := t.UploadExceptV4Headers(ctx, memfileCfg, rootfsCfg)
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
