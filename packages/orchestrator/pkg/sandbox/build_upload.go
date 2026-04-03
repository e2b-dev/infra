package sandbox

import (
	"context"
	"fmt"
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
func NewBuildUploader(snapshot *Snapshot, persistence storage.StorageProvider, paths storage.Paths, cfg *storage.CompressConfig, pending *PendingBuildInfo) BuildUploader {
	base := buildUploader{
		paths:       paths,
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
	paths       storage.Paths
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

func (b *buildUploader) uploadUncompressedFile(ctx context.Context, local, remote string, objType storage.SeekableObjectType) error {
	object, err := b.persistence.OpenSeekable(ctx, remote, objType)
	if err != nil {
		return err
	}

	if _, _, err := object.StoreFile(ctx, local, nil); err != nil {
		return fmt.Errorf("error when uploading %s: %w", remote, err)
	}

	return nil
}

// Snap-file is small enough so we don't use composite upload.
func (b *buildUploader) uploadSnapfile(ctx context.Context, path string) error {
	object, err := b.persistence.OpenBlob(ctx, b.paths.Snapfile(), storage.SnapfileObjectType)
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
	object, err := b.persistence.OpenBlob(ctx, b.paths.Metadata(), storage.MetadataObjectType)
	if err != nil {
		return err
	}

	if err := uploadFileAsBlob(ctx, object, path); err != nil {
		return fmt.Errorf("error when uploading metadata: %w", err)
	}

	return nil
}

func (b *buildUploader) uploadCompressedFile(ctx context.Context, local, remote string, objType storage.SeekableObjectType, cfg *storage.CompressConfig) (*storage.FrameTable, [32]byte, error) {
	object, err := b.persistence.OpenSeekable(ctx, remote, objType)
	if err != nil {
		return nil, [32]byte{}, fmt.Errorf("error opening framed file for %s: %w", remote, err)
	}

	ft, checksum, err := object.StoreFile(ctx, local, cfg)
	if err != nil {
		return nil, [32]byte{}, fmt.Errorf("error compressing %s to %s: %w", local, remote, err)
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

	// Track frame cursor per build to avoid O(N²) rescanning.
	cursors := make(map[string]int)

	for _, mapping := range h.Mapping {
		key := pendingBuildInfoKey(mapping.BuildId.String(), fileType)
		info := p.get(key)

		if info == nil {
			continue
		}

		cursor := cursors[key]
		next, err := mapping.SetFramesFrom(info.ft, cursor)
		if err != nil {
			return fmt.Errorf("apply frames to mapping at offset %d for build %s: %w",
				mapping.Offset, mapping.BuildId.String(), err)
		}
		cursors[key] = next

		// Populate BuildFiles with size and checksum for this build.
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
