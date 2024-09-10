package storage

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	blockStorage "github.com/e2b-dev/infra/packages/block-storage/pkg"
	"github.com/e2b-dev/infra/packages/shared/pkg/template"

	"cloud.google.com/go/storage"
	"github.com/jellydator/ttlcache/v3"
)

const (
	templateDataExpiration = time.Hour * 25
	pageSize               = 2 << 11
	hugepageSize           = 2 << 20
	// TODO: Extract shared constants.
	templateCacheDir = "/template/cache"
)

type TemplateData struct {
	paths *template.TemplateFiles

	Memfile *blockStorage.BlockStorage

	ensureOpen func() (*TemplateData, error)
}

func (t *TemplateData) Close() error {
	return t.Memfile.Close()
}

func newTemplateData(ctx context.Context, client *storage.Client, bucket, templateId, buildId string, hugePages bool) *TemplateData {
	h := &TemplateData{
		paths: template.NewTemplateFiles(templateId, buildId),
	}

	h.ensureOpen = sync.OnceValues(func() (*TemplateData, error) {
		dirKey := filepath.Join(templateId, buildId)
		fileKey := filepath.Join(dirKey, template.MemfileName)

		memfileObject := blockStorage.NewBucketObject(
			ctx,
			client,
			bucket,
			fileKey,
		)

		dirPath := filepath.Join(templateCacheDir, dirKey)

		err := os.MkdirAll(dirPath, os.ModePerm)
		if err != nil {
			return nil, fmt.Errorf("failed to create directory %s: %w", dirPath, err)
		}

		cachePath := filepath.Join(dirPath, template.MemfileName)

		var blockSize int64
		if hugePages {
			blockSize = hugepageSize
		} else {
			blockSize = pageSize
		}

		memfileStorage, err := blockStorage.New(
			ctx,
			memfileObject,
			cachePath,
			blockSize,
		)

		h.Memfile = memfileStorage

		return h, err
	})

	return h
}

type TemplateDataCache struct {
	cache         *ttlcache.Cache[string, *TemplateData]
	storageClient *storage.Client
	ctx           context.Context
	bucket        string
}

func (t *TemplateDataCache) GetTemplateData(templateID, buildID string, hugePages bool) (*TemplateData, error) {
	id := fmt.Sprintf("%s-%s", templateID, buildID)

	templateData, _ := t.cache.GetOrSet(
		id,
		newTemplateData(t.ctx, t.storageClient, t.bucket, templateID, buildID, hugePages),
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
		bucket:        bucket,
		cache:         cache,
		storageClient: client,
		ctx:           ctx,
	}
}
