package sandbox

import (
	"bytes"
	"context"
	"fmt"
	"sync"

	"github.com/google/uuid"
	"golang.org/x/sync/errgroup"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

// PendingFrameTables collects frame tables from data uploads, keyed by
// object name (e.g., "buildId/rootfs.ext4"). These are held temporarily
// until headers can be finalized after all data uploads complete.
type PendingFrameTables struct {
	tables sync.Map // key: objectName (string), value: *storage.FrameTable
}

func NewPendingFrameTables() *PendingFrameTables {
	return &PendingFrameTables{}
}

// Add stores a frame table for a specific object name.
func (p *PendingFrameTables) Add(objectName string, ft *storage.FrameTable) {
	if ft == nil {
		return
	}
	p.tables.Store(objectName, ft)
}

// Get retrieves a frame table for a specific object name.
func (p *PendingFrameTables) Get(objectName string) *storage.FrameTable {
	v, ok := p.tables.Load(objectName)
	if !ok {
		return nil
	}

	return v.(*storage.FrameTable)
}

// ApplyToHeader applies frame tables to all mappings in a header based on each mapping's BuildId.
// This should be called after all data uploads are complete so all frame tables are available.
func (p *PendingFrameTables) ApplyToHeader(h *header.Header, fileType string) error {
	if h == nil {
		return nil
	}

	for _, mapping := range h.Mapping {
		if mapping.BuildId == uuid.Nil {
			// Skip hole mappings
			continue
		}

		objectName := mapping.BuildId.String() + "/" + fileType
		ft := p.Get(objectName)
		if ft == nil {
			// No frame table for this build - data might be uncompressed or already has FT
			continue
		}

		if err := mapping.AddFrames(ft); err != nil {
			return fmt.Errorf("failed to add frames to mapping for build %s: %w", mapping.BuildId, err)
		}
	}

	return nil
}

type TemplateBuild struct {
	files       storage.TemplateFiles
	persistence storage.StorageProvider

	memfileHeader *header.Header
	rootfsHeader  *header.Header
}

func NewTemplateBuild(memfileHeader *header.Header, rootfsHeader *header.Header, s storage.StorageProvider, files storage.TemplateFiles) *TemplateBuild {
	return &TemplateBuild{
		persistence: s,
		files:       files,

		memfileHeader: memfileHeader,
		rootfsHeader:  rootfsHeader,
	}
}

func (t *TemplateBuild) Remove(ctx context.Context) error {
	err := t.persistence.DeleteWithPrefix(ctx, t.files.StorageDir())
	if err != nil {
		return fmt.Errorf("error when removing template build '%s': %w", t.files.StorageDir(), err)
	}

	return nil
}

// DataUploadResult contains the frame tables generated from uploading data files.
type DataUploadResult struct {
	MemfileFrameTable *storage.FrameTable
	RootfsFrameTable  *storage.FrameTable
}

// UploadData uploads all data files (rootfs, memfile, snapfile, metadata) in parallel.
// It returns the frame tables generated from compression, which should be added
// to PendingFrameTables for later use when finalizing headers.
func (t *TemplateBuild) UploadData(ctx context.Context, metadataPath string, fcSnapfilePath string, memfilePath *string, rootfsPath *string) (*DataUploadResult, error) {
	eg, ctx := errgroup.WithContext(ctx)

	result := &DataUploadResult{}
	var rootfsFTMu, memfileFTMu sync.Mutex

	eg.Go(func() error {
		// RootFS data
		if rootfsPath != nil {
			ft, err := t.persistence.StoreFile(ctx, *rootfsPath, t.files.StorageRootfsPath(), storage.DefaultCompressionOptions)
			if err != nil {
				return fmt.Errorf("error when uploading rootfs data: %w", err)
			}
			rootfsFTMu.Lock()
			result.RootfsFrameTable = ft
			rootfsFTMu.Unlock()
		}

		return nil
	})

	eg.Go(func() error {
		// Memfile data
		if memfilePath != nil {
			ft, err := t.persistence.StoreFile(ctx, *memfilePath, t.files.StorageMemfilePath(), storage.DefaultCompressionOptions)
			if err != nil {
				return fmt.Errorf("error when uploading memfile data: %w", err)
			}
			memfileFTMu.Lock()
			result.MemfileFrameTable = ft
			memfileFTMu.Unlock()
		}

		return nil
	})

	eg.Go(func() error {
		// Snap file
		err := storage.StoreBlobFromFile(ctx, t.persistence, fcSnapfilePath, t.files.StorageSnapfilePath())
		if err != nil {
			return fmt.Errorf("error when uploading snapfile: %w", err)
		}

		return nil
	})

	eg.Go(func() error {
		// Metadata
		err := storage.StoreBlobFromFile(ctx, t.persistence, metadataPath, t.files.StorageMetadataPath())
		if err != nil {
			return fmt.Errorf("error when uploading metadata: %w", err)
		}

		return nil
	})

	if err := eg.Wait(); err != nil {
		return nil, err
	}

	return result, nil
}

// FinalizeHeaders applies pending frame tables to headers, serializes them,
// and uploads them to storage. This should be called after all data uploads are complete
// so pending contains frame tables from all builds referenced by the headers.
func (t *TemplateBuild) FinalizeHeaders(ctx context.Context, pending *PendingFrameTables) error {
	eg, ctx := errgroup.WithContext(ctx)

	eg.Go(func() error {
		if t.rootfsHeader == nil {
			return nil
		}

		if err := pending.ApplyToHeader(t.rootfsHeader, "rootfs.ext4"); err != nil {
			return fmt.Errorf("failed to apply frame tables to rootfs header: %w", err)
		}

		serialized, err := header.Serialize(t.rootfsHeader.Metadata, t.rootfsHeader.Mapping)
		if err != nil {
			return fmt.Errorf("error when serializing rootfs header: %w", err)
		}

		err = t.persistence.StoreBlob(ctx, t.files.StorageRootfsHeaderPath(), bytes.NewReader(serialized))
		if err != nil {
			return fmt.Errorf("error when uploading rootfs header: %w", err)
		}

		return nil
	})

	eg.Go(func() error {
		if t.memfileHeader == nil {
			return nil
		}

		if err := pending.ApplyToHeader(t.memfileHeader, "memfile"); err != nil {
			return fmt.Errorf("failed to apply frame tables to memfile header: %w", err)
		}

		serialized, err := header.Serialize(t.memfileHeader.Metadata, t.memfileHeader.Mapping)
		if err != nil {
			return fmt.Errorf("error when serializing memfile header: %w", err)
		}

		err = t.persistence.StoreBlob(ctx, t.files.StorageMemfileHeaderPath(), bytes.NewReader(serialized))
		if err != nil {
			return fmt.Errorf("error when uploading memfile header: %w", err)
		}

		return nil
	})

	return eg.Wait()
}

// Upload uploads data files and headers for a single build.
// This is appropriate for single-layer uploads (e.g., pausing a sandbox) where
// parent frame tables are already embedded in the header from previous builds.
// For parallel multi-layer builds, use UploadData + FinalizeHeaders with a shared
// PendingFrameTables to coordinate frame tables across concurrent uploads.
func (t *TemplateBuild) Upload(ctx context.Context, metadataPath string, fcSnapfilePath string, memfilePath *string, rootfsPath *string) chan error {
	done := make(chan error)

	go func() {
		// Create pending frame tables just for this build
		pending := NewPendingFrameTables()

		// Upload data files
		result, err := t.UploadData(ctx, metadataPath, fcSnapfilePath, memfilePath, rootfsPath)
		if err != nil {
			done <- err

			return
		}

		// Add this build's frame tables to pending
		buildId := t.files.BuildID
		if result.RootfsFrameTable != nil {
			pending.Add(buildId+"/rootfs.ext4", result.RootfsFrameTable)
		}
		if result.MemfileFrameTable != nil {
			pending.Add(buildId+"/memfile", result.MemfileFrameTable)
		}

		// Finalize headers (only has this build's frame tables, not parents')
		// This is why this method is deprecated - use the two-phase approach instead
		err = t.FinalizeHeaders(ctx, pending)
		done <- err
	}()

	return done
}
