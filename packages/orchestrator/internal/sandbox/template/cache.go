package template

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/jellydator/ttlcache/v3"
	"go.uber.org/zap"

	blockmetrics "github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block/metrics"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/build"
	"github.com/e2b-dev/infra/packages/shared/pkg/env"
	featureflags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

// How long to keep the template in the cache since the last access.
// Should be longer than the maximum possible sandbox lifetime.
const (
	templateExpiration = time.Hour * 25

	buildCacheTTL           = time.Hour * 25
	buildCacheDelayEviction = time.Second * 60

	// buildCacheMaxUsedPercentage the maximum percentage of the cache disk storage
	// that can be used before the cache starts evicting items.
	buildCacheMaxUsedPercentage = 75.0
)

type Cache struct {
	flags         *featureflags.Client
	cache         *ttlcache.Cache[string, Template]
	persistence   storage.StorageProvider
	buildStore    *build.DiffStore
	blockMetrics  blockmetrics.Metrics
	rootCachePath string
}

// NewCache initializes a template new cache.
// It also deletes the old build cache directory content
// as it may contain stale data that are not managed by anyone.
func NewCache(
	ctx context.Context,
	flags *featureflags.Client,
	persistence storage.StorageProvider,
	metrics blockmetrics.Metrics,
) (*Cache, error) {
	cache := ttlcache.New(
		ttlcache.WithTTL[string, Template](templateExpiration),
	)

	cache.OnEviction(func(ctx context.Context, reason ttlcache.EvictionReason, item *ttlcache.Item[string, Template]) {
		template := item.Value()

		err := template.Close()
		if err != nil {
			zap.L().Warn("failed to cleanup template data", zap.String("item_key", item.Key()), zap.Error(err))
		}
	})

	// Delete the old build cache directory content.
	err := cleanDir(build.DefaultCachePath)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("failed to remove old build cache directory: %w", err)
	}

	buildStore, err := build.NewDiffStore(
		ctx,
		build.DefaultCachePath,
		buildCacheTTL,
		buildCacheDelayEviction,
		buildCacheMaxUsedPercentage,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create build store: %w", err)
	}

	go cache.Start()

	return &Cache{
		blockMetrics:  metrics,
		persistence:   persistence,
		buildStore:    buildStore,
		cache:         cache,
		flags:         flags,
		rootCachePath: env.GetEnv("SHARED_CHUNK_CACHE_PATH", ""),
	}, nil
}

func (c *Cache) Items() map[string]*ttlcache.Item[string, Template] {
	return c.cache.Items()
}

func (c *Cache) GetTemplate(
	ctx context.Context,
	buildID,
	kernelVersion,
	firecrackerVersion string,
	isSnapshot bool,
	isBuilding bool,
) (Template, error) {
	persistence := c.persistence
	// Because of the template caching, if we enable the shared cache feature flag,
	// it will start working only for new orchestrators or new builds.
	if c.useNFSCache(isBuilding, isSnapshot) {
		zap.L().Info("using local template cache", zap.String("path", c.rootCachePath))
		persistence = storage.NewCachedProvider(c.rootCachePath, persistence)
	}

	storageTemplate, err := newTemplateFromStorage(
		buildID,
		kernelVersion,
		firecrackerVersion,
		nil,
		nil,
		persistence,
		c.blockMetrics,
		nil,
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create template cache from storage: %w", err)
	}

	t, found := c.cache.GetOrSet(
		storageTemplate.Files().CacheKey(),
		storageTemplate,
		ttlcache.WithTTL[string, Template](templateExpiration),
	)

	if !found {
		go storageTemplate.Fetch(ctx, c.buildStore)
	}

	return t.Value(), nil
}

func (c *Cache) AddSnapshot(
	ctx context.Context,
	buildId,
	kernelVersion,
	firecrackerVersion string,
	memfileHeader *header.Header,
	rootfsHeader *header.Header,
	localSnapfile File,
	localMetafile File,
	memfileDiff build.Diff,
	rootfsDiff build.Diff,
) error {
	switch memfileDiff.(type) {
	case *build.NoDiff:
		break
	default:
		c.buildStore.Add(memfileDiff)
	}

	switch rootfsDiff.(type) {
	case *build.NoDiff:
		break
	default:
		c.buildStore.Add(rootfsDiff)
	}

	storageTemplate, err := newTemplateFromStorage(
		buildId,
		kernelVersion,
		firecrackerVersion,
		memfileHeader,
		rootfsHeader,
		c.persistence,
		c.blockMetrics,
		localSnapfile,
		localMetafile,
	)
	if err != nil {
		return fmt.Errorf("failed to create template cache from storage: %w", err)
	}

	_, found := c.cache.GetOrSet(
		storageTemplate.Files().CacheKey(),
		storageTemplate,
		ttlcache.WithTTL[string, Template](templateExpiration),
	)

	if !found {
		go storageTemplate.Fetch(ctx, c.buildStore)
	}

	return nil
}

func (c *Cache) useNFSCache(isBuilding bool, isSnapshot bool) bool {
	if isBuilding {
		// caching this layer doesn't speed up the next sandbox launch,
		// as the previous template isn't used to load the oen that's being built.
		return false
	}

	if c.rootCachePath == "" {
		// can't enable cache if we don't have a cache path
		return false
	}

	var flagName featureflags.BoolFlag
	if isSnapshot {
		flagName = featureflags.SnapshotFeatureFlagName
	} else {
		flagName = featureflags.TemplateFeatureFlagName
	}

	flag, err := c.flags.BoolFlag(flagName, "")
	if err != nil {
		zap.L().Error("failed to get nfs cache feature flag", zap.Error(err))
	}

	return flag
}

func cleanDir(path string) error {
	entries, err := os.ReadDir(path)
	if err != nil {
		return fmt.Errorf("reading directory contents: %w", err)
	}

	for _, entry := range entries {
		entryPath := filepath.Join(path, entry.Name())
		if err := os.RemoveAll(entryPath); err != nil {
			return fmt.Errorf("removing %q: %w", entryPath, err)
		}
	}

	return nil
}
