package sandbox

import (
	"bytes"
	"context"
	"fmt"
	"os"

	"golang.org/x/sync/errgroup"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	headers "github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

type TemplateBuild struct {
	files       storage.TemplateFiles
	persistence storage.API

	memfileHeader *headers.Header
	rootfsHeader  *headers.Header
}

func NewTemplateBuild(memfileHeader *headers.Header, rootfsHeader *headers.Header, persistence storage.API, files storage.TemplateFiles) *TemplateBuild {
	return &TemplateBuild{
		persistence: persistence,
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

func (t *TemplateBuild) uploadMemfileHeader(ctx context.Context, h *headers.Header) error {
	serialized, err := headers.Serialize(h.Metadata, h.Mapping)
	if err != nil {
		return fmt.Errorf("error when serializing memfile header: %w", err)
	}

	err = t.persistence.StoreBlob(ctx, t.files.StorageMemfileHeaderPath(), bytes.NewReader(serialized))
	if err != nil {
		return fmt.Errorf("error when uploading memfile header: %w", err)
	}

	return nil
}

func (t *TemplateBuild) uploadMemfile(ctx context.Context, memfilePath string) (*storage.FrameTable, error) {
	return t.persistence.StoreFile(ctx, memfilePath, t.files.StorageMemfilePath(), nil)
}

func (t *TemplateBuild) uploadRootfsHeader(ctx context.Context, h *headers.Header) error {
	serialized, err := headers.Serialize(h.Metadata, h.Mapping)
	if err != nil {
		return fmt.Errorf("error when serializing rootFS header: %w", err)
	}

	err = t.persistence.StoreBlob(ctx, t.files.StorageRootfsHeaderPath(), bytes.NewReader(serialized))
	if err != nil {
		return err
	}

	return nil
}

func (t *TemplateBuild) uploadRootfs(ctx context.Context, rootfsPath string) (*storage.FrameTable, error) {
	return t.persistence.StoreFile(ctx, rootfsPath, t.files.StorageRootfsPath(), nil)
}

// Snap-file is small enough so we don't use composite upload.
func (t *TemplateBuild) uploadSnapfile(ctx context.Context, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("failed to open file %s: %w", path, err)
	}
	defer f.Close()

	err = t.persistence.StoreBlob(ctx, t.files.StorageSnapfilePath(), f)
	if err != nil {
		return err
	}

	return nil
}

// Metadata is small enough so we don't use composite upload.
func (t *TemplateBuild) uploadMetadata(ctx context.Context, localFilePath string) error {
	f, err := os.Open(localFilePath)
	if err != nil {
		return fmt.Errorf("failed to open file %s: %w", localFilePath, err)
	}
	defer f.Close()

	err = t.persistence.StoreBlob(ctx, t.files.StorageMetadataPath(), f)
	if err != nil {
		return err
	}

	return nil
}

func (t *TemplateBuild) Upload(ctx context.Context, metadataPath string, fcSnapfilePath string, memfilePath *string, rootfsPath *string) chan error {
	eg, ctx := errgroup.WithContext(ctx)

	eg.Go(func() error {
		if rootfsPath == nil {
			return nil
		}

		_, err := t.uploadRootfs(ctx, *rootfsPath)
		if err != nil {
			return err
		}

		if t.rootfsHeader == nil {
			return nil
		}

		// if frameTable != nil {
		// 	fmt.Printf("<>/<> Uploading build %q rootFS, full size %#x, have a frame table starting at %#x, %d frames\n",
		// 		t.rootfsHeader.Metadata.BuildId.String(),
		// 		t.rootfsHeader.Metadata.Size,
		// 		frameTable.StartAt.U,
		// 		len(frameTable.Frames),
		// 	) // DEBUG --- IGNORE ---
		// 	for _, f := range frameTable.Frames {
		// 		fmt.Printf("<>/<> --- frame: %#x %#x\n", f.U, f.C) // DEBUG --- IGNORE ---
		// 	}

		// 	// iterate over the mappings, and for each one from the current build add the frameTable
		// 	for _, mapping := range t.rootfsHeader.Mapping {
		// 		if mapping.BuildId == t.rootfsHeader.Metadata.BuildId {
		// 			mapping.FrameTable = frameTable.Subset(int64(mapping.Offset), int64(mapping.Length))

		// 			if len(mapping.FrameTable.Frames) == 0 {
		// 				fmt.Printf("<>/<>   NO FRAMES for mapping offset %#x length %#x\n",
		// 					mapping.Offset,
		// 					mapping.Length,
		// 				) // DEBUG --- IGNORE ---

		// 				fmt.Printf("<>/<> full mapping table: type %s, offset: %+v\n", storage.CompressionType(mapping.FrameTable.CompressionType), mapping.FrameTable.StartAt) // DEBUG --- IGNORE ---

		// 				for _, f := range mapping.FrameTable.Frames {
		// 					fmt.Printf("<>/<>     frame: %+v\n", f) // DEBUG --- IGNORE ---
		// 				}
		// 			}
		// 		}
		// 	}
		// }

		err = t.uploadRootfsHeader(ctx, t.rootfsHeader)
		if err != nil {
			return err
		}

		return nil
	})

	eg.Go(func() error {
		if memfilePath == nil {
			return nil
		}

		_, err := t.uploadMemfile(ctx, *memfilePath)
		if err != nil {
			return err
		}

		if t.memfileHeader == nil {
			return nil
		}

		// if frameTable != nil {
		// 	fmt.Printf("<>/<> Uploading build %q memfile, have a frame table starting at %#x, %d frames\n",
		// 		t.memfileHeader.Metadata.BuildId.String(),
		// 		frameTable.StartAt.U,
		// 		len(frameTable.Frames),
		// 	) // DEBUG --- IGNORE ---

		// 	// iterate over the mappings, and for each one from the current build add the f info
		// 	for _, mapping := range t.memfileHeader.Mapping {
		// 		if mapping.BuildId == t.memfileHeader.Metadata.BuildId {
		// 			mapping.FrameTable = frameTable.Subset(int64(mapping.Offset), int64(mapping.Length))
		// 		}
		// 	}
		// }

		err = t.uploadMemfileHeader(ctx, t.memfileHeader)
		if err != nil {
			return err
		}

		return nil
	})

	eg.Go(func() error {
		if err := t.uploadSnapfile(ctx, fcSnapfilePath); err != nil {
			return fmt.Errorf("error when uploading snapfile: %w", err)
		}

		return nil
	})

	eg.Go(func() error {
		return t.uploadMetadata(ctx, metadataPath)
	})

	done := make(chan error)

	go func() {
		done <- eg.Wait()
	}()

	return done
}
