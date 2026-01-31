package template

import (
	"context"
	"errors"
	"fmt"

	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/cfg"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	blockmetrics "github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block/metrics"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/build"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/metadata"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
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
	config cfg.BuilderConfig,
	buildId string,
	memfileHeader *header.Header,
	rootfsHeader *header.Header,
	persistence storage.StorageProvider,
	metrics blockmetrics.Metrics,
	localSnapfile File,
	localMetafile File,
) (*storageTemplate, error) {
	files, err := storage.TemplateFiles{
		BuildID: buildId,
	}.CacheFiles(config.StorageConfig)
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
	ctx, span := tracer.Start(ctx, "fetch storage template", trace.WithAttributes(
		telemetry.WithBuildID(t.files.BuildID),
	))
	defer span.End()

	var wg errgroup.Group

	wg.Go(func() error {
		if t.localSnapfile != nil {
			if err := t.snapfile.SetValue(t.localSnapfile); err != nil {
				return fmt.Errorf("failed to set local snapfile: %w", err)
			}

			return nil
		}

		snapfile, snapfileErr := newStorageFile(
			ctx,
			t.persistence,
			t.files.StorageSnapfilePath(),
			t.files.CacheSnapfilePath(),
		)
		if snapfileErr != nil {
			errMsg := fmt.Errorf("failed to fetch snapfile: %w", snapfileErr)

			if err := t.snapfile.SetError(errMsg); err != nil {
				return fmt.Errorf("failed to set snapfile error: %w", errors.Join(errMsg, err))
			}

			return nil
		}

		if err := t.snapfile.SetValue(snapfile); err != nil {
			return fmt.Errorf("failed to set snapfile: %w", err)
		}

		return nil
	})

	wg.Go(func() error {
		if t.localMetafile != nil {
			if err := t.metafile.SetValue(t.localMetafile); err != nil {
				return fmt.Errorf("failed to set local metafile: %w", err)
			}

			return nil
		}

		meta, err := newStorageFile(
			ctx,
			t.persistence,
			t.files.StorageMetadataPath(),
			t.files.CacheMetadataPath(),
		)
		if err != nil && !errors.Is(err, storage.ErrObjectNotExist) {
			sourceErr := fmt.Errorf("failed to fetch metafile: %w", err)
			if err := t.metafile.SetError(sourceErr); err != nil {
				return fmt.Errorf("failed to set metafile error: %w", errors.Join(sourceErr, err))
			}

			return nil
		}

		if err != nil {
			// If we can't find the metadata, we still want to return the metafile.
			// This is used for templates that don't have metadata, like v1 templates.
			logger.L().Info(ctx, "failed to fetch metafile, falling back to v1 template metadata",
				logger.WithBuildID(t.files.BuildID),
				zap.Error(err),
			)
			oldTemplateMetadata := metadata.V1TemplateVersion()
			err := oldTemplateMetadata.ToFile(t.files.CacheMetadataPath())
			if err != nil {
				sourceErr := fmt.Errorf("failed to write v1 template metadata to a file: %w", err)
				if err := t.metafile.SetError(sourceErr); err != nil {
					return fmt.Errorf("failed to set metafile error: %w", errors.Join(sourceErr, err))
				}

				return nil
			}

			if err := t.metafile.SetValue(&storageFile{
				path: t.files.CacheMetadataPath(),
			}); err != nil {
				return fmt.Errorf("failed to set metafile v1: %w", err)
			}

			return nil
		}

		if err := t.metafile.SetValue(meta); err != nil {
			return fmt.Errorf("failed to set metafile value: %w", err)
		}

		return nil
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

			if err := t.memfile.SetError(errMsg); err != nil {
				return fmt.Errorf("failed to set memfile error: %w", errors.Join(errMsg, err))
			}

			return nil
		}

		if err := t.memfile.SetValue(memfileStorage); err != nil {
			return fmt.Errorf("failed to set memfile value: %w", err)
		}

		return nil
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
			errMsg := fmt.Errorf("failed to create rootfs storage for build %s: %w", t.files.BuildID, rootfsErr)

			if err := t.rootfs.SetError(errMsg); err != nil {
				return fmt.Errorf("failed to set rootfs error: %w", errors.Join(errMsg, err))
			}

			return nil
		}

		if err := t.rootfs.SetValue(rootfsStorage); err != nil {
			return fmt.Errorf("failed to set rootfs value: %w", err)
		}

		return nil
	})

	err := wg.Wait()
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())

		logger.L().Error(ctx, "failed to fetch template files",
			logger.WithBuildID(t.files.BuildID),
			zap.Error(err),
		)

		return
	}
}

func (t *storageTemplate) Close(ctx context.Context) error {
	return closeTemplate(ctx, t)
}

func (t *storageTemplate) Files() storage.TemplateCacheFiles {
	return t.files
}

func (t *storageTemplate) Memfile(ctx context.Context) (block.ReadonlyDevice, error) {
	_, span := tracer.Start(ctx, "storage-template-memfile")
	defer span.End()

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
