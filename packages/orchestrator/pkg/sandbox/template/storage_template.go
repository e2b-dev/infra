package template

import (
	"context"
	"errors"
	"fmt"

	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/cfg"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block"
	blockmetrics "github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block/metrics"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/build"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/template/metadata"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

type storageTemplate struct {
	paths storage.CachePaths

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
	paths, err := storage.Paths{
		BuildID: buildId,
	}.Cache(config.StorageConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create cache paths: %w", err)
	}

	return &storageTemplate{
		paths:         paths,
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

func (t *storageTemplate) Fetch(ctx context.Context, buildStore *build.DiffStore) error {
	ctx, span := tracer.Start(ctx, "fetch storage template", trace.WithAttributes(
		telemetry.WithBuildID(t.paths.BuildID),
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
			t.paths.Snapfile(),
			t.paths.CacheSnapfile(),
			storage.SnapfileObjectType,
		)
		if snapfileErr != nil {
			errMsg := fmt.Errorf("failed to fetch snapfile: %w", snapfileErr)

			if err := t.snapfile.SetError(errMsg); err != nil {
				return fmt.Errorf("failed to set snapfile error: %w", errors.Join(errMsg, err))
			}

			return errMsg
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
			t.paths.Metadata(),
			t.paths.CacheMetadata(),
			storage.MetadataObjectType,
		)
		if err != nil && !errors.Is(err, storage.ErrObjectNotExist) {
			sourceErr := fmt.Errorf("failed to fetch metafile: %w", err)
			if err := t.metafile.SetError(sourceErr); err != nil {
				return fmt.Errorf("failed to set metafile error: %w", errors.Join(sourceErr, err))
			}

			return sourceErr
		}

		if err != nil {
			// If we can't find the metadata, we still want to return the metafile.
			// This is used for templates that don't have metadata, like v1 templates.
			logger.L().Info(ctx, "failed to fetch metafile, falling back to v1 template metadata",
				logger.WithBuildID(t.paths.BuildID),
				zap.Error(err),
			)
			oldTemplateMetadata := metadata.V1TemplateVersion()
			err := oldTemplateMetadata.ToFile(t.paths.CacheMetadata())
			if err != nil {
				sourceErr := fmt.Errorf("failed to write v1 template metadata to a file: %w", err)
				if err := t.metafile.SetError(sourceErr); err != nil {
					return fmt.Errorf("failed to set metafile error: %w", errors.Join(sourceErr, err))
				}

				return sourceErr
			}

			if err := t.metafile.SetValue(&storageFile{
				path: t.paths.CacheMetadata(),
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
			t.paths.BuildID,
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

			return errMsg
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
			t.paths.BuildID,
			build.Rootfs,
			t.rootfsHeader,
			t.persistence,
			t.metrics,
		)
		if rootfsErr != nil {
			errMsg := fmt.Errorf("failed to create rootfs storage: %w", rootfsErr)

			if err := t.rootfs.SetError(errMsg); err != nil {
				return fmt.Errorf("failed to set rootfs error: %w", errors.Join(errMsg, err))
			}

			return errMsg
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

		return err
	}

	return nil
}

func (t *storageTemplate) Close(ctx context.Context) error {
	return closeTemplate(ctx, t)
}

func (t *storageTemplate) Files() storage.CachePaths {
	return t.paths
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

func (t *storageTemplate) UpdateMetadata(meta metadata.Template) error {
	metafile, err := t.metafile.Wait()
	if err != nil {
		return fmt.Errorf("failed to get metafile: %w", err)
	}

	return meta.ToFile(metafile.Path())
}
