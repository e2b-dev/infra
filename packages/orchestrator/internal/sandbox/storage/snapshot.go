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
	hugepageSize           = 2 << 20
	// TODO: Extract shared constants.
	memfileName = "memfile"
)

type SnapshotData struct {
	memfile    *blockStorage.BlockStorage
	EnsureOpen func() (*SnapshotData, error)
}

func (s *SnapshotData) Close() error {
	return s.memfile.Close()
}

func newTemplateData(ctx context.Context, client *storage.Client, bucket, templateId, buildId string) *SnapshotData {
	h := &SnapshotData{}

	h.EnsureOpen = sync.OnceValues(func() (*SnapshotData, error) {
		fileKey := filepath.Join(templateId, buildId, memfileName)

		memfileObject := blockStorage.NewBucketObject(
			ctx,
			client,
			bucket,
			fileKey,
		)

		memfileStorage, err := blockStorage.New(
			ctx,
			memfileObject,
			filepath.Join(os.TempDir(), fileKey),
			hugepageSize,
		)

		h.memfile = memfileStorage

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

func (c *SnapshotDataCache) GetTemplateData(templateID, buildID string) (*SnapshotData, error) {
	id := fmt.Sprintf("%s-%s", templateID, buildID)

	snapshotData, _ := c.cache.GetOrSet(
		id,
		newTemplateData(c.ctx, c.storageClient, c.bucket, templateID, buildID),
		ttlcache.WithTTL[string, *SnapshotData](snapshotDataExpiration),
	)

	mp, err := snapshotData.Value().EnsureOpen()
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
