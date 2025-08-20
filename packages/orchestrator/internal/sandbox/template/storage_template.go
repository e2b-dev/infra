package template

import (
	"context"
	"errors"
	"fmt"

	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	blockmetrics "github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block/metrics"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/build"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/metadata"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

type storageTemplate struct {
	files storage.TemplateCacheFiles

	memfile  *utils.SetOnce[block.ReadonlyDevice]
	rootfs   *utils.SetOnce[block.ReadonlyDevice]
	snapfile *utils.SetOnce[File]
	metafile *utils.SetOnce[File]

	memfileHeader *header.Header
	rootfsHeader  *header.Header
	localSnapfile File
	localMetafile File

	metrics     blockmetrics.Metrics
	persistence storage.StorageProvider
}

func newTemplateFromStorage(
	buildId,
	kernelVersion,
	firecrackerVersion string,
	memfileHeader *header.Header,
	rootfsHeader *header.Header,
	persistence storage.StorageProvider,
	metrics blockmetrics.Metrics,
	localSnapfile File,
	localMetafile File,
) (*storageTemplate, error) {
	files, err := storage.TemplateFiles{
		BuildID:            buildId,
		KernelVersion:      kernelVersion,
		FirecrackerVersion: firecrackerVersion,
	}.CacheFiles()
	if err != nil {
		return nil, fmt.Errorf("failed to create template cache files: %w", err)
	}

	return &storageTemplate{
		files:         files,
		localSnapfile: localSnapfile,
		localMetafile: localMetafile,
		memfileHeader: memfileHeader,
		rootfsHeader:  rootfsHeader,
		metrics:       metrics,
		persistence:   persistence,
		memfile:       utils.NewSetOnce[block.ReadonlyDevice](),
		rootfs:        utils.NewSetOnce[block.ReadonlyDevice](),
		snapfile:      utils.NewSetOnce[File](),
		metafile:      utils.NewSetOnce[File](),
	}, nil
}

func (t *storageTemplate) Fetch(ctx context.Context, buildStore *build.DiffStore) {
	var wg errgroup.Group

	wg.Go(func() error {
		if t.localSnapfile != nil {
			return t.snapfile.SetValue(t.localSnapfile)
		}

		snapfile, snapfileErr := newStorageFile(
			ctx,
			t.persistence,
			t.files.StorageSnapfilePath(),
			t.files.CacheSnapfilePath(),
		)
		if snapfileErr != nil {
			errMsg := fmt.Errorf("failed to fetch snapfile: %w", snapfileErr)

			return t.snapfile.SetError(errMsg)
		}

		return t.snapfile.SetValue(snapfile)
	})

	wg.Go(func() error {
		if t.localMetafile != nil {
			return t.metafile.SetValue(t.localMetafile)
		}

		meta, err := newStorageFile(
			ctx,
			t.persistence,
			t.files.StorageMetadataPath(),
			t.files.CacheMetadataPath(),
		)
		if err != nil && !errors.Is(err, storage.ErrorObjectNotExist) {
			return t.metafile.SetError(fmt.Errorf("failed to fetch metafile: %w", err))
		}

		if err != nil {
			// If we can't find the metadata, we still want to return the metafile.
			// This is used for templates that don't have metadata, like v1 templates.
			zap.L().Info("failed to fetch metafile, falling back to v1 template metadata",
				logger.WithBuildID(t.files.BuildID),
				zap.Error(err),
			)
			oldTemplateMetadata := metadata.Template{
				Version:  1,
				Template: t.files.TemplateFiles,
			}
			err := oldTemplateMetadata.ToFile(t.files.CacheMetadataPath())
			if err != nil {
				return t.metafile.SetError(fmt.Errorf("failed to write v1 template metadata to a file: %w", err))
			}

			return t.metafile.SetValue(&storageFile{
				path: t.files.CacheMetadataPath(),
			})
		}

		return t.metafile.SetValue(meta)
	})

	wg.Go(func() error {
		memfileStorage, memfileErr := NewStorage(
			ctx,
			buildStore,
			t.files.BuildID,
			build.Memfile,
			t.memfileHeader,
			t.persistence,
			t.metrics,
		)

		if memfileErr != nil {
			errMsg := fmt.Errorf("failed to create memfile storage: %w", memfileErr)

			return t.memfile.SetError(errMsg)
		}

		return t.memfile.SetValue(memfileStorage)
	})

	wg.Go(func() error {
		rootfsStorage, rootfsErr := NewStorage(
			ctx,
			buildStore,
			t.files.BuildID,
			build.Rootfs,
			t.rootfsHeader,
			t.persistence,
			t.metrics,
		)
		if rootfsErr != nil {
			errMsg := fmt.Errorf("failed to create rootfs storage: %w", rootfsErr)

			return t.rootfs.SetError(errMsg)
		}

		return t.rootfs.SetValue(rootfsStorage)
	})

	err := wg.Wait()
	if err != nil {
		zap.L().Error("failed to fetch template files",
			zap.String("build_id", t.files.BuildID),
			zap.Error(err),
		)
		return
	}
}

func (t *storageTemplate) Close() error {
	return closeTemplate(t)
}

func (t *storageTemplate) Files() storage.TemplateCacheFiles {
	return t.files
}

func (t *storageTemplate) Memfile() (block.ReadonlyDevice, error) {
	return t.memfile.Wait()
}

func (t *storageTemplate) Rootfs() (block.ReadonlyDevice, error) {
	return t.rootfs.Wait()
}

func (t *storageTemplate) Snapfile() (File, error) {
	return t.snapfile.Wait()
}

func (t *storageTemplate) Metadata() (metadata.Template, error) {
	metafile, err := t.metafile.Wait()
	if err != nil {
		return metadata.Template{}, fmt.Errorf("failed to get metafile: %w", err)
	}

	return metadata.FromFile(metafile.Path())
}

func (t *storageTemplate) ReplaceMemfile(memfile block.ReadonlyDevice) error {
	m := utils.NewSetOnce[block.ReadonlyDevice]()
	m.SetValue(memfile)
	t.memfile = m
	return nil
}
