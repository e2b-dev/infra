package storage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	blockStorage "github.com/e2b-dev/infra/packages/block-storage/pkg"
	templateStorage "github.com/e2b-dev/infra/packages/shared/pkg/storage"

	"cloud.google.com/go/storage"
	"github.com/jellydator/ttlcache/v3"
)

const (
	templateDataExpiration = time.Hour * 72
	pageSize               = 2 << 11
	hugepageSize           = 2 << 20
	rootfsBlockSize        = 2 << 11
)

type TemplateData struct {
	Memfile  *blockStorage.BlockStorage
	Snapfile *SimpleFile
	Rootfs   *blockStorage.BlockStorage

	ensureOpen func() (*TemplateData, error)

	Files *templateStorage.TemplateFiles
}

func (t *TemplateData) Close() error {
	var errs []error

	if t.Memfile != nil {
		errs = append(errs, t.Memfile.Close())
	}

	if t.Rootfs != nil {
		errs = append(errs, t.Rootfs.Close())
	}

	if t.Snapfile != nil {
		errs = append(errs, t.Snapfile.Remove())
	}

	return errors.Join(errs...)
}

func newTemplateData(
	ctx context.Context,
	bucket *storage.BucketHandle,
	templateId,
	buildId,
	kernelVersion,
	firecrackerVersion string,
	hugePages bool,
) *TemplateData {
	h := &TemplateData{
		Files: templateStorage.NewTemplateFiles(templateId, buildId, kernelVersion, firecrackerVersion),
	}

	h.ensureOpen = sync.OnceValues(func() (*TemplateData, error) {
		err := os.MkdirAll(h.Files.CacheDir(), os.ModePerm)
		if err != nil {
			return nil, fmt.Errorf("failed to create directory %s: %w", h.Files.CacheDir(), err)
		}

		h.Snapfile = NewSimpleFile(ctx, bucket, h.Files.StorageSnapfilePath(), h.Files.CacheSnapfilePath())

		// Asynchronously start the file download.
		go h.Snapfile.GetPath()

		memfileObject := blockStorage.NewBucketObject(
			ctx,
			bucket,
			h.Files.StorageMemfilePath(),
		)

		var memfileBlockSize int64
		if hugePages {
			memfileBlockSize = hugepageSize
		} else {
			memfileBlockSize = pageSize
		}

		memfileStorage, err := blockStorage.New(
			ctx,
			memfileObject,
			h.Files.CacheMemfilePath(),
			memfileBlockSize,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to create memfile storage: %w", err)
		}

		h.Memfile = memfileStorage

		rootfsObject := blockStorage.NewBucketObject(
			ctx,
			bucket,
			h.Files.StorageRootfsPath(),
		)

		rootfsStorage, err := blockStorage.New(
			ctx,
			rootfsObject,
			h.Files.CacheRootfsPath(),
			rootfsBlockSize,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to create rootfs storage: %w", err)
		}

		h.Rootfs = rootfsStorage

		return h, nil
	})

	return h
}

type TemplateDataCache struct {
	cache  *ttlcache.Cache[string, *TemplateData]
	bucket *storage.BucketHandle
	ctx    context.Context
}

func (t *TemplateDataCache) GetTemplateData(
	templateId,
	buildId,
	kernelVersion,
	firecrackerVersion string,
	hugePages bool,
) (*TemplateData, error) {
	id := fmt.Sprintf("%s-%s", templateId, buildId)

	templateData, _ := t.cache.GetOrSet(
		id,
		newTemplateData(t.ctx, t.bucket, templateId, buildId, kernelVersion, firecrackerVersion, hugePages),
		ttlcache.WithTTL[string, *TemplateData](templateDataExpiration),
	)

	mp, err := templateData.Value().ensureOpen()
	if err != nil {
		t.cache.Delete(id)

		return nil, fmt.Errorf("failed to create template data cache %s: %w", id, err)
	}

	return mp, nil
}

func NewTemplateDataCache(ctx context.Context, client *storage.Client, bucket string) *TemplateDataCache {
	b := client.Bucket(bucket)

	cache := ttlcache.New(
		ttlcache.WithTTL[string, *TemplateData](templateDataExpiration),
	)

	cache.OnEviction(func(ctx context.Context, reason ttlcache.EvictionReason, item *ttlcache.Item[string, *TemplateData]) {
		data := item.Value()

		err := data.Close()
		if err != nil {
			fmt.Printf("failed to cleanup template data for item %s: %v", item.Key(), err)
		}
	})

	go cache.Start()

	return &TemplateDataCache{
		bucket: b,
		cache:  cache,
		ctx:    ctx,
	}
}
