package storage

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	blockStorage "github.com/e2b-dev/infra/packages/block-storage/pkg"

	"cloud.google.com/go/storage"
	"github.com/jellydator/ttlcache/v3"
)

const (
	snapshotDataExpiration = time.Hour * 25
	pageSize               = 2 << 11
	hugepageSize           = 2 << 20
	// TODO: Extract shared constants.
	memfileName      = "memfile"
	snapshotCacheDir = "/snapshots/cache"
)

type SnapshotData struct {
	Memfile    *blockStorage.BlockStorage
	ensureOpen func() (*SnapshotData, error)
}

func (s *SnapshotData) Close() error {
	return s.Memfile.Close()
}

func newTemplateData(ctx context.Context, client *storage.Client, bucket, templateId, buildId string, hugePages bool) *SnapshotData {
	h := &SnapshotData{}

	h.ensureOpen = sync.OnceValues(func() (*SnapshotData, error) {
		dirKey := filepath.Join(templateId, buildId)
		fileKey := filepath.Join(dirKey, memfileName)

		memfileObject := blockStorage.NewBucketObject(
			ctx,
			client,
			bucket,
			fileKey,
		)

		dirPath := filepath.Join(snapshotCacheDir, dirKey)

		err := os.MkdirAll(dirPath, os.ModePerm)
		if err != nil {
			return nil, fmt.Errorf("failed to create directory %s: %w", dirPath, err)
		}

		cachePath := filepath.Join(dirPath, memfileName)

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

type SnapshotDataCache struct {
	cache         *ttlcache.Cache[string, *SnapshotData]
	storageClient *storage.Client
	ctx           context.Context
	bucket        string
}

func (c *SnapshotDataCache) GetTemplateData(templateID, buildID string, hugePages bool) (*SnapshotData, error) {
	id := fmt.Sprintf("%s-%s", templateID, buildID)

	snapshotData, _ := c.cache.GetOrSet(
		id,
		newTemplateData(c.ctx, c.storageClient, c.bucket, templateID, buildID, hugePages),
		ttlcache.WithTTL[string, *SnapshotData](snapshotDataExpiration),
	)

	mp, err := snapshotData.Value().ensureOpen()
	if err != nil {
		c.cache.Delete(id)

		return nil, fmt.Errorf("failed to create snapshot data %s: %w", id, err)
	}

	return mp, nil
}

func NewSnapshotDataCache(ctx context.Context, client *storage.Client, bucket string) *SnapshotDataCache {
	cache := ttlcache.New(
		ttlcache.WithTTL[string, *SnapshotData](snapshotDataExpiration),
	)

	cache.OnEviction(func(ctx context.Context, reason ttlcache.EvictionReason, item *ttlcache.Item[string, *SnapshotData]) {
		data := item.Value()

		err := data.Close()
		if err != nil {
			fmt.Printf("failed to cleanup snapshot data for item %s: %v", item.Key(), err)
		}
	})

	go cache.Start()

	return &SnapshotDataCache{
		bucket:        bucket,
		cache:         cache,
		storageClient: client,
		ctx:           ctx,
	}
}
