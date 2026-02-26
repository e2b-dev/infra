package sandbox

import (
	"context"
	"fmt"
	"io"
	"os"

	"golang.org/x/sync/errgroup"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/compress"
	headers "github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

type TemplateBuild struct {
	files          storage.TemplateFiles
	persistence    storage.StorageProvider
	compressConfig *compress.Config

	memfileHeader *headers.Header
	rootfsHeader  *headers.Header
}

func NewTemplateBuild(memfileHeader *headers.Header, rootfsHeader *headers.Header, persistence storage.StorageProvider, files storage.TemplateFiles, compressConfig *compress.Config) *TemplateBuild {
	// Deep-copy headers: the upload may add frame tables (for compression)
	// and must not mutate the originals shared with the template cache.
	copyHeader := func(h *headers.Header) *headers.Header {
		if h == nil {
			return nil
		}
		meta := *h.Metadata
		m := make([]*headers.BuildMap, len(h.Mapping))
		for i, bm := range h.Mapping {
			m[i] = bm.Copy()
		}
		cp, _ := headers.NewHeader(&meta, m)
		return cp
	}

	return &TemplateBuild{
		persistence:    persistence,
		files:          files,
		compressConfig: compressConfig,

		memfileHeader: copyHeader(memfileHeader),
		rootfsHeader:  copyHeader(rootfsHeader),
	}
}

func (t *TemplateBuild) Remove(ctx context.Context) error {
	err := t.persistence.DeleteObjectsWithPrefix(ctx, t.files.StorageDir())
	if err != nil {
		return fmt.Errorf("error when removing template build '%s': %w", t.files.StorageDir(), err)
	}

	return nil
}

func (t *TemplateBuild) uploadHeader(ctx context.Context, h *headers.Header, path string, objectType storage.ObjectType) error {
	object, err := t.persistence.OpenBlob(ctx, path, objectType)
	if err != nil {
		return err
	}

	serialized, err := headers.SerializeWithFrames(h.Metadata, h.Mapping, h.FrameTables)
	if err != nil {
		return fmt.Errorf("error serializing header: %w", err)
	}

	return object.Put(ctx, serialized)
}

// uploadDataCompressed compresses localPath into storagePath using CompressData
// and returns the frame info for the header.
func (t *TemplateBuild) uploadDataCompressed(ctx context.Context, localPath, storagePath string, objectType storage.SeekableObjectType) ([]compress.FrameInfo, error) {
	src, err := os.Open(localPath)
	if err != nil {
		return nil, fmt.Errorf("open source: %w", err)
	}
	defer src.Close()

	// For local FS: write to the storage path directly.
	// For GCS: write to a temp buffer then upload.
	// The simplest universal approach: write to a temp file, then StoreFile(raw).
	tmpFile, err := os.CreateTemp("", "compress-*")
	if err != nil {
		return nil, fmt.Errorf("create temp: %w", err)
	}
	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()

	frames, err := compress.CompressData(ctx, src, tmpFile, t.compressConfig)
	if err != nil {
		return nil, fmt.Errorf("compress: %w", err)
	}
	tmpFile.Close()

	// Upload the compressed data as raw (no additional compression).
	obj, err := t.persistence.OpenSeekable(ctx, storagePath, objectType)
	if err != nil {
		return nil, fmt.Errorf("open storage: %w", err)
	}

	if err := obj.StoreFile(ctx, tmpFile.Name()); err != nil {
		return nil, fmt.Errorf("upload: %w", err)
	}

	return frames, nil
}

func (t *TemplateBuild) uploadDataUncompressed(ctx context.Context, localPath, storagePath string, objectType storage.SeekableObjectType) error {
	obj, err := t.persistence.OpenSeekable(ctx, storagePath, objectType)
	if err != nil {
		return err
	}

	return obj.StoreFile(ctx, localPath)
}

func (t *TemplateBuild) Upload(ctx context.Context, metadataPath string, fcSnapfilePath string, memfilePath *string, rootfsPath *string) chan error {
	eg, ctx := errgroup.WithContext(ctx)

	// Phase 1: Upload data files (+ compress if configured).
	// If compressed, this produces frame tables that get attached to headers.
	eg.Go(func() error {
		if memfilePath == nil {
			return nil
		}

		if t.compressConfig != nil && t.memfileHeader != nil {
			frames, err := t.uploadDataCompressed(ctx, *memfilePath, t.files.StorageMemfilePath(), storage.MemfileObjectType)
			if err != nil {
				return fmt.Errorf("compress+upload memfile: %w", err)
			}
			ft := headers.NewFrameTable(t.memfileHeader.Metadata.BuildId, frames)
			t.memfileHeader.SetFrameTable(ft)
		} else {
			if err := t.uploadDataUncompressed(ctx, *memfilePath, t.files.StorageMemfilePath(), storage.MemfileObjectType); err != nil {
				return fmt.Errorf("upload memfile: %w", err)
			}
		}

		return nil
	})

	eg.Go(func() error {
		if rootfsPath == nil {
			return nil
		}

		if t.compressConfig != nil && t.rootfsHeader != nil {
			frames, err := t.uploadDataCompressed(ctx, *rootfsPath, t.files.StorageRootfsPath(), storage.RootFSObjectType)
			if err != nil {
				return fmt.Errorf("compress+upload rootfs: %w", err)
			}
			ft := headers.NewFrameTable(t.rootfsHeader.Metadata.BuildId, frames)
			t.rootfsHeader.SetFrameTable(ft)
		} else {
			if err := t.uploadDataUncompressed(ctx, *rootfsPath, t.files.StorageRootfsPath(), storage.RootFSObjectType); err != nil {
				return fmt.Errorf("upload rootfs: %w", err)
			}
		}

		return nil
	})

	eg.Go(func() error {
		return uploadFileAsBlob(ctx, t.persistence, t.files.StorageSnapfilePath(), storage.SnapfileObjectType, fcSnapfilePath)
	})

	eg.Go(func() error {
		return uploadFileAsBlob(ctx, t.persistence, t.files.StorageMetadataPath(), storage.MetadataObjectType, metadataPath)
	})

	// Phase 1 must complete before phase 2 (headers need frame tables from data upload).
	done := make(chan error)
	go func() {
		if err := eg.Wait(); err != nil {
			done <- err
			return
		}

		// Phase 2: Upload headers (now with frame tables attached).
		eg2, ctx2 := errgroup.WithContext(ctx)

		if t.memfileHeader != nil {
			eg2.Go(func() error {
				return t.uploadHeader(ctx2, t.memfileHeader, t.files.StorageMemfileHeaderPath(), storage.MemfileHeaderObjectType)
			})
		}
		if t.rootfsHeader != nil {
			eg2.Go(func() error {
				return t.uploadHeader(ctx2, t.rootfsHeader, t.files.StorageRootfsHeaderPath(), storage.RootFSHeaderObjectType)
			})
		}

		done <- eg2.Wait()
	}()

	return done
}

func uploadFileAsBlob(ctx context.Context, persistence storage.StorageProvider, path string, objectType storage.ObjectType, localPath string) error {
	object, err := persistence.OpenBlob(ctx, path, objectType)
	if err != nil {
		return err
	}

	f, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("open %s: %w", localPath, err)
	}
	defer f.Close()

	data, err := io.ReadAll(f)
	if err != nil {
		return fmt.Errorf("read %s: %w", localPath, err)
	}

	return object.Put(ctx, data)
}
