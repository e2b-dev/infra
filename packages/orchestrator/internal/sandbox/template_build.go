package sandbox

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync"

	"golang.org/x/sync/errgroup"

	featureflags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	headers "github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

type TemplateBuild struct {
	files       storage.TemplateFiles
	persistence storage.StorageProvider
	ff          *featureflags.Client

	memfileHeader *headers.Header
	rootfsHeader  *headers.Header
}

func NewTemplateBuild(memfileHeader *headers.Header, rootfsHeader *headers.Header, persistence storage.StorageProvider, files storage.TemplateFiles, ff *featureflags.Client) *TemplateBuild {
	return &TemplateBuild{
		persistence: persistence,
		files:       files,
		ff:          ff,

		memfileHeader: memfileHeader,
		rootfsHeader:  rootfsHeader,
	}
}

func (t *TemplateBuild) Remove(ctx context.Context) error {
	err := t.persistence.DeleteObjectsWithPrefix(ctx, t.files.StorageDir())
	if err != nil {
		return fmt.Errorf("error when removing template build '%s': %w", t.files.StorageDir(), err)
	}

	return nil
}

func (t *TemplateBuild) uploadMemfileHeaderV3(ctx context.Context, h *headers.Header) error {
	object, err := t.persistence.OpenBlob(ctx, t.files.StorageMemfileHeaderPath())
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

func (t *TemplateBuild) uploadMemfile(ctx context.Context, memfilePath string) error {
	object, err := t.persistence.OpenFramedFile(ctx, t.files.StorageMemfilePath())
	if err != nil {
		return err
	}

	if _, err := object.StoreFile(ctx, memfilePath, nil); err != nil {
		return fmt.Errorf("error when uploading memfile: %w", err)
	}

	return nil
}

func (t *TemplateBuild) uploadRootfsHeaderV3(ctx context.Context, h *headers.Header) error {
	object, err := t.persistence.OpenBlob(ctx, t.files.StorageRootfsHeaderPath())
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

func (t *TemplateBuild) uploadRootfs(ctx context.Context, rootfsPath string) error {
	object, err := t.persistence.OpenFramedFile(ctx, t.files.StorageRootfsPath())
	if err != nil {
		return err
	}

	if _, err := object.StoreFile(ctx, rootfsPath, nil); err != nil {
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
// This includes: V3 headers, uncompressed data, compressed data (when enabled via
// feature flag), snapfile, and metadata. Returns the frame tables from compressed
// uploads for later use in V4 header serialization. Non-nil frame tables indicate
// that compression was enabled.
func (t *TemplateBuild) UploadExceptV4Headers(
	ctx context.Context,
	metadataPath string,
	fcSnapfilePath string,
	memfilePath *string,
	rootfsPath *string,
) (memFT, rootFT *storage.FrameTable, err error) {
	compressOpts := storage.GetUploadOptions(ctx, t.ff)
	eg, ctx := errgroup.WithContext(ctx)

	// Uncompressed headers (always)
	eg.Go(func() error {
		if t.rootfsHeader == nil {
			return nil
		}

		return t.uploadRootfsHeaderV3(ctx, t.rootfsHeader)
	})

	eg.Go(func() error {
		if t.memfileHeader == nil {
			return nil
		}

		return t.uploadMemfileHeaderV3(ctx, t.memfileHeader)
	})

	// Uncompressed data (always, for rollback safety)
	eg.Go(func() error {
		if rootfsPath == nil {
			return nil
		}

		return t.uploadRootfs(ctx, *rootfsPath)
	})

	eg.Go(func() error {
		if memfilePath == nil {
			return nil
		}

		return t.uploadMemfile(ctx, *memfilePath)
	})

	// Compressed data (when enabled)
	if compressOpts != nil {
		var memFTMu, rootFTMu sync.Mutex

		if memfilePath != nil {
			eg.Go(func() error {
				ft, err := t.uploadCompressed(ctx, *memfilePath, storage.MemfileName, compressOpts)
				if err != nil {
					return fmt.Errorf("compressed memfile upload: %w", err)
				}

				memFTMu.Lock()
				memFT = ft
				memFTMu.Unlock()

				return nil
			})
		}

		if rootfsPath != nil {
			eg.Go(func() error {
				ft, err := t.uploadCompressed(ctx, *rootfsPath, storage.RootfsName, compressOpts)
				if err != nil {
					return fmt.Errorf("compressed rootfs upload: %w", err)
				}

				rootFTMu.Lock()
				rootFT = ft
				rootFTMu.Unlock()

				return nil
			})
		}
	}

	// Snapfile + metadata
	eg.Go(func() error {
		return t.uploadSnapfile(ctx, fcSnapfilePath)
	})

	eg.Go(func() error {
		return t.uploadMetadata(ctx, metadataPath)
	})

	if err := eg.Wait(); err != nil {
		return nil, nil, err
	}

	return memFT, rootFT, nil
}

// uploadCompressed compresses and uploads a file to the compressed data path.
func (t *TemplateBuild) uploadCompressed(ctx context.Context, localPath, fileName string, opts *storage.FramedUploadOptions) (*storage.FrameTable, error) {
	objectPath := t.files.CompressedDataPath(fileName, opts.CompressionType)

	object, err := t.persistence.OpenFramedFile(ctx, objectPath)
	if err != nil {
		return nil, fmt.Errorf("error opening framed file for %s: %w", objectPath, err)
	}

	ft, err := object.StoreFile(ctx, localPath, opts)
	if err != nil {
		return nil, fmt.Errorf("error compressing %s to %s: %w", fileName, objectPath, err)
	}

	return ft, nil
}

// applyFrameTablesForBuild applies a frame table directly to a header's mappings
// by matching buildID — no PendingFrameTables map indirection needed.
func applyFrameTablesForBuild(h *headers.Header, buildID string, ft *storage.FrameTable) error {
	if h == nil || ft == nil {
		return nil
	}

	for _, mapping := range h.Mapping {
		if mapping.BuildId.String() == buildID {
			return mapping.AddFrames(ft)
		}
	}

	return nil // no matching mapping (e.g. NoDiff)
}

// serializeAndUploadHeader serializes a header as v4 compressed format, LZ4-compresses it,
// and uploads to the compressed header path.
func (t *TemplateBuild) serializeAndUploadHeader(ctx context.Context, h *headers.Header, fileType string) error {
	meta := *h.Metadata
	meta.Version = headers.MetadataVersionCompressed

	serialized, err := headers.Serialize(&meta, h.Mapping)
	if err != nil {
		return fmt.Errorf("serialize compressed %s header: %w", fileType, err)
	}

	compressed, err := storage.CompressLZ4(serialized)
	if err != nil {
		return fmt.Errorf("compress %s header: %w", fileType, err)
	}

	objectPath := t.files.CompressedHeaderPath(fileType)
	blob, err := t.persistence.OpenBlob(ctx, objectPath)
	if err != nil {
		return fmt.Errorf("open blob for compressed %s header: %w", fileType, err)
	}

	if err := blob.Put(ctx, compressed); err != nil {
		return fmt.Errorf("upload compressed %s header: %w", fileType, err)
	}

	return nil
}

// uploadCompressedHeadersForBuild uploads compressed headers for a single-layer build,
// applying frame tables directly without PendingFrameTables indirection.
func (t *TemplateBuild) uploadCompressedHeadersForBuild(ctx context.Context, memFT, rootFT *storage.FrameTable) error {
	buildID := t.files.BuildID
	eg, ctx := errgroup.WithContext(ctx)

	if t.memfileHeader != nil {
		eg.Go(func() error {
			if err := applyFrameTablesForBuild(t.memfileHeader, buildID, memFT); err != nil {
				return fmt.Errorf("apply frames to memfile header: %w", err)
			}

			return t.serializeAndUploadHeader(ctx, t.memfileHeader, storage.MemfileName)
		})
	}

	if t.rootfsHeader != nil {
		eg.Go(func() error {
			if err := applyFrameTablesForBuild(t.rootfsHeader, buildID, rootFT); err != nil {
				return fmt.Errorf("apply frames to rootfs header: %w", err)
			}

			return t.serializeAndUploadHeader(ctx, t.rootfsHeader, storage.RootfsName)
		})
	}

	return eg.Wait()
}

// UploadCompressedHeaders serializes the v4 compressed headers (with frame tables)
// and uploads them. Used by the multi-layer build path where PendingFrameTables
// collects frame tables across all layers.
func (t *TemplateBuild) UploadCompressedHeaders(ctx context.Context, pending *PendingFrameTables) error {
	eg, ctx := errgroup.WithContext(ctx)

	if t.memfileHeader != nil {
		eg.Go(func() error {
			return t.uploadCompressedHeaderWithPending(ctx, pending, t.memfileHeader, storage.MemfileName)
		})
	}

	if t.rootfsHeader != nil {
		eg.Go(func() error {
			return t.uploadCompressedHeaderWithPending(ctx, pending, t.rootfsHeader, storage.RootfsName)
		})
	}

	return eg.Wait()
}

func (t *TemplateBuild) uploadCompressedHeaderWithPending(
	ctx context.Context,
	pending *PendingFrameTables,
	h *headers.Header,
	fileType string,
) error {
	if err := pending.ApplyToHeader(h, fileType); err != nil {
		return fmt.Errorf("apply frames to %s header: %w", fileType, err)
	}

	return t.serializeAndUploadHeader(ctx, h, fileType)
}

// UploadAll uploads data files and headers for a single build (e.g., sandbox pause).
// When compression is enabled (via feature flag), compressed data + compressed headers
// are also uploaded. For multi-layer builds, use UploadExceptV4Headers +
// UploadCompressedHeaders with a shared PendingFrameTables instead.
func (t *TemplateBuild) UploadAll(ctx context.Context, metadataPath string, fcSnapfilePath string, memfilePath *string, rootfsPath *string) chan error {
	done := make(chan error, 1)

	go func() {
		memFT, rootFT, err := t.UploadExceptV4Headers(ctx, metadataPath, fcSnapfilePath, memfilePath, rootfsPath)
		if err != nil {
			done <- err

			return
		}

		// Finalize compressed headers if compression was enabled.
		if memFT != nil || rootFT != nil {
			if err := t.uploadCompressedHeadersForBuild(ctx, memFT, rootFT); err != nil {
				done <- fmt.Errorf("error uploading compressed headers: %w", err)

				return
			}
		}

		done <- nil
	}()

	return done
}
