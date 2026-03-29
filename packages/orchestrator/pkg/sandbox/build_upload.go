package sandbox

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/google/uuid"
	"golang.org/x/sync/errgroup"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/build"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	headers "github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

// BuildUploader uploads a paused snapshot's files to storage.
type BuildUploader interface {
	// UploadData uploads data files, snapfile, and metadata.
	UploadData(ctx context.Context) error
	// FinalizeHeaders uploads final headers after all upstream layers are done.
	// Returns serialized V4 header bytes for peer transition (nil for uncompressed).
	FinalizeHeaders(ctx context.Context) (memfileHeader, rootfsHeader []byte, err error)
}

// NewBuildUploader creates a BuildUploader for the given snapshot.
// If cfg is non-nil, compression is used (V4 headers). Otherwise, uncompressed (V3 headers).
// pending is shared across layers for multi-layer builds; nil is fine for single-layer.
func NewBuildUploader(snapshot *Snapshot, persistence storage.StorageProvider, files storage.TemplateFiles, cfg *storage.CompressConfig, pending *PendingBuildInfo) BuildUploader {
	base := buildUploader{
		files:       files,
		persistence: persistence,
		snapshot:    snapshot,
	}

	if cfg != nil {
		if pending == nil {
			pending = &PendingBuildInfo{}
		}

		return &compressedUploader{
			buildUploader: base,
			pending:       pending,
			cfg:           cfg,
		}
	}

	return &uncompressedUploader{buildUploader: base}
}

// buildUploader contains fields and helpers shared by both implementations.
type buildUploader struct {
	files       storage.TemplateFiles
	persistence storage.StorageProvider
	snapshot    *Snapshot
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

func (b *buildUploader) uploadUncompressedFile(ctx context.Context, localPath, fileName string) error {
	object, err := b.persistence.OpenFramedFile(ctx, b.files.DataPath(fileName))
	if err != nil {
		return err
	}

	if _, _, err := object.StoreFile(ctx, localPath, nil); err != nil {
		return fmt.Errorf("error when uploading %s: %w", fileName, err)
	}

	return nil
}

// Snap-file is small enough so we don't use composite upload.
func (b *buildUploader) uploadSnapfile(ctx context.Context, path string) error {
	object, err := b.persistence.OpenBlob(ctx, b.files.StorageSnapfilePath(), storage.SnapfileObjectType)
	if err != nil {
		return err
	}

	if err = uploadFileAsBlob(ctx, object, path); err != nil {
		return fmt.Errorf("error when uploading snapfile: %w", err)
	}

	return nil
}

// Metadata is small enough so we don't use composite upload.
func (b *buildUploader) uploadMetadata(ctx context.Context, path string) error {
	object, err := b.persistence.OpenBlob(ctx, b.files.StorageMetadataPath(), storage.MetadataObjectType)
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

func (b *buildUploader) uploadCompressedFile(ctx context.Context, localPath, fileName string, cfg *storage.CompressConfig) (*storage.FrameTable, [32]byte, error) {
	objectPath := b.files.CompressedDataPath(fileName, cfg.CompressionType())

	object, err := b.persistence.OpenFramedFile(ctx, objectPath)
	if err != nil {
		return nil, [32]byte{}, fmt.Errorf("error opening framed file for %s: %w", objectPath, err)
	}

	ft, checksum, err := object.StoreFile(ctx, localPath, cfg)
	if err != nil {
		return nil, [32]byte{}, fmt.Errorf("error compressing %s to %s: %w", fileName, objectPath, err)
	}

	return ft, checksum, nil
}

func (b *buildUploader) scheduleAlwaysUploads(eg *errgroup.Group, ctx context.Context) {
	eg.Go(func() error {
		return b.uploadSnapfile(ctx, b.snapshot.Snapfile.Path())
	})

	eg.Go(func() error {
		return b.uploadMetadata(ctx, b.snapshot.Metafile.Path())
	})
}

// --- Uncompressed (V3) implementation ---

type uncompressedUploader struct {
	buildUploader
}

func (u *uncompressedUploader) UploadData(ctx context.Context) error {
	memfilePath, err := diffPath(u.snapshot.MemfileDiff)
	if err != nil {
		return fmt.Errorf("error getting memfile diff path: %w", err)
	}

	rootfsPath, err := diffPath(u.snapshot.RootfsDiff)
	if err != nil {
		return fmt.Errorf("error getting rootfs diff path: %w", err)
	}

	eg, ctx := errgroup.WithContext(ctx)

	// V3 headers
	eg.Go(func() error {
		if u.snapshot.MemfileDiffHeader == nil {
			return nil
		}

		_, err := headers.StoreHeader(ctx, u.persistence, u.files.HeaderPath(storage.MemfileName), u.snapshot.MemfileDiffHeader)

		return err
	})

	eg.Go(func() error {
		if u.snapshot.RootfsDiffHeader == nil {
			return nil
		}

		_, err := headers.StoreHeader(ctx, u.persistence, u.files.HeaderPath(storage.RootfsName), u.snapshot.RootfsDiffHeader)

		return err
	})

	// Uncompressed data
	eg.Go(func() error {
		if memfilePath == nil {
			return nil
		}

		return u.uploadUncompressedFile(ctx, *memfilePath, storage.MemfileName)
	})

	eg.Go(func() error {
		if rootfsPath == nil {
			return nil
		}

		return u.uploadUncompressedFile(ctx, *rootfsPath, storage.RootfsName)
	})

	u.scheduleAlwaysUploads(eg, ctx)

	return eg.Wait()
}

func (u *uncompressedUploader) FinalizeHeaders(context.Context) ([]byte, []byte, error) {
	return nil, nil, nil
}

// --- Compressed (V4) implementation ---

type compressedUploader struct {
	buildUploader

	pending *PendingBuildInfo
	cfg     *storage.CompressConfig
}

func (c *compressedUploader) UploadData(ctx context.Context) error {
	memfilePath, err := diffPath(c.snapshot.MemfileDiff)
	if err != nil {
		return fmt.Errorf("error getting memfile diff path: %w", err)
	}

	rootfsPath, err := diffPath(c.snapshot.RootfsDiff)
	if err != nil {
		return fmt.Errorf("error getting rootfs diff path: %w", err)
	}

	eg, ctx := errgroup.WithContext(ctx)

	if memfilePath != nil {
		localPath := *memfilePath
		eg.Go(func() error {
			ft, checksum, err := c.uploadCompressedFile(ctx, localPath, storage.MemfileName, c.cfg)
			if err != nil {
				return fmt.Errorf("compressed memfile upload: %w", err)
			}

			uncompressedSize, _ := ft.Size()
			c.pending.add(pendingBuildInfoKey(c.files.BuildID, storage.MemfileName), ft, uncompressedSize, checksum)

			return nil
		})
	}

	if rootfsPath != nil {
		localPath := *rootfsPath
		eg.Go(func() error {
			ft, checksum, err := c.uploadCompressedFile(ctx, localPath, storage.RootfsName, c.cfg)
			if err != nil {
				return fmt.Errorf("compressed rootfs upload: %w", err)
			}

			uncompressedSize, _ := ft.Size()
			c.pending.add(pendingBuildInfoKey(c.files.BuildID, storage.RootfsName), ft, uncompressedSize, checksum)

			return nil
		})
	}

	c.scheduleAlwaysUploads(eg, ctx)

	return eg.Wait()
}

// FinalizeHeaders applies pending frame tables to headers and uploads them as V4 format.
//
// The snapshot headers are cloned before mutation because the originals may be
// concurrently read by sandboxes resumed from the template cache (e.g. the
// optimize phase's UFFD handlers).
func (c *compressedUploader) FinalizeHeaders(ctx context.Context) (memfileHeader, rootfsHeader []byte, err error) {
	eg, ctx := errgroup.WithContext(ctx)

	if c.snapshot.MemfileDiffHeader != nil {
		eg.Go(func() error {
			h := c.snapshot.MemfileDiffHeader.CloneForUpload()

			if err := c.pending.applyToHeader(h, storage.MemfileName); err != nil {
				return fmt.Errorf("apply frames to memfile header: %w", err)
			}

			h.Metadata.Version = headers.MetadataVersionCompressed

			data, err := headers.StoreHeader(ctx, c.persistence, c.files.HeaderPath(storage.MemfileName), h)
			if err != nil {
				return err
			}

			memfileHeader = data

			return nil
		})
	}

	if c.snapshot.RootfsDiffHeader != nil {
		eg.Go(func() error {
			h := c.snapshot.RootfsDiffHeader.CloneForUpload()

			if err := c.pending.applyToHeader(h, storage.RootfsName); err != nil {
				return fmt.Errorf("apply frames to rootfs header: %w", err)
			}

			h.Metadata.Version = headers.MetadataVersionCompressed

			data, err := headers.StoreHeader(ctx, c.persistence, c.files.HeaderPath(storage.RootfsName), h)
			if err != nil {
				return err
			}

			rootfsHeader = data

			return nil
		})
	}

	if err = eg.Wait(); err != nil {
		return nil, nil, err
	}

	return memfileHeader, rootfsHeader, nil
}

// pendingBuildInfo pairs a FrameTable with the uncompressed file size and
// uncompressed-data checksum so all can be stored in the header after uploads complete.
type pendingBuildInfo struct {
	ft       *storage.FrameTable
	fileSize int64
	checksum [32]byte
}

// PendingBuildInfo collects FrameTables and file sizes from compressed data
// uploads across all layers. After all data files are uploaded, the collected
// tables are applied to headers before the compressed headers are serialized
// and uploaded.
type PendingBuildInfo sync.Map

func pendingBuildInfoKey(buildID, fileType string) string {
	return buildID + "/" + fileType
}

func (p *PendingBuildInfo) add(key string, ft *storage.FrameTable, fileSize int64, checksum [32]byte) {
	if ft == nil {
		return
	}

	(*sync.Map)(p).Store(key, pendingBuildInfo{ft: ft, fileSize: fileSize, checksum: checksum})
}

func (p *PendingBuildInfo) get(key string) *pendingBuildInfo {
	v, ok := (*sync.Map)(p).Load(key)
	if !ok {
		return nil
	}

	info, ok := v.(pendingBuildInfo)
	if !ok {
		return nil
	}

	return &info
}

func (p *PendingBuildInfo) applyToHeader(h *headers.Header, fileType string) error {
	if h == nil {
		return nil
	}

	for _, mapping := range h.Mapping {
		key := pendingBuildInfoKey(mapping.BuildId.String(), fileType)
		info := p.get(key)

		if info == nil {
			continue
		}

		if err := mapping.AddFrames(info.ft); err != nil {
			return fmt.Errorf("apply frames to mapping at offset %#x for build %s: %w",
				mapping.Offset, mapping.BuildId.String(), err)
		}
	}

	// Populate BuildFiles with sizes and checksums for this fileType's builds.
	for _, mapping := range h.Mapping {
		key := pendingBuildInfoKey(mapping.BuildId.String(), fileType)
		info := p.get(key)
		if info == nil {
			continue
		}

		if h.BuildFiles == nil {
			h.BuildFiles = make(map[uuid.UUID]headers.BuildFileInfo)
		}
		h.BuildFiles[mapping.BuildId] = headers.BuildFileInfo{
			Size:     info.fileSize,
			Checksum: info.checksum,
		}
	}

	return nil
}
