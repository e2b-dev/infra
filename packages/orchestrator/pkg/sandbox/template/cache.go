//go:build linux

package template

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/jellydator/ttlcache/v3"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/cfg"
	blockmetrics "github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block/metrics"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/build"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/template/peerclient"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/template/metadata"
	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

// How long to keep the template in the cache since the last access.
// Should be longer than the maximum possible sandbox lifetime.
const (
	templateExpiration       = time.Hour * 25
	templateExpirationBuffer = time.Hour

	buildCacheTTL           = time.Hour * 25
	buildCacheDelayEviction = time.Second * 60
)

var (
	tracer     = otel.Tracer("github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/template")
	meter      = otel.Meter("github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/template")
	hitsMetric = utils.Must(meter.Int64Counter("orchestrator.templates.cache.hits",
		metric.WithDescription("Requests for templates that were already cached")))
	missesMetric = utils.Must(meter.Int64Counter("orchestrator.templates.cache.misses",
		metric.WithDescription("Requests for templates that were not cached")))
)

type Cache struct {
	config        cfg.Config
	flags         *featureflags.Client
	cache         *ttlcache.Cache[string, Template]
	persistence   storage.StorageProvider
	buildStore    *build.DiffStore
	blockMetrics  blockmetrics.Metrics
	rootCachePath string
	peers         peerclient.Resolver
	extendMu      sync.Mutex
}

// NewCache initializes a template new cache.
// It also deletes the old build cache directory content
// as it may contain stale data that are not managed by anyone.
func NewCache(
	config cfg.Config,
	flags *featureflags.Client,
	persistence storage.StorageProvider,
	metrics blockmetrics.Metrics,
	peers peerclient.Resolver,
) (*Cache, error) {
	cacheOpts := []ttlcache.Option[string, Template]{
		ttlcache.WithTTL[string, Template](templateExpiration),
	}
	if config.TemplateCacheMaxEntries > 0 {
		cacheOpts = append(cacheOpts, ttlcache.WithCapacity[string, Template](uint64(config.TemplateCacheMaxEntries)))
	}
	cache := ttlcache.New(cacheOpts...)

	cache.OnEviction(func(ctx context.Context, _ ttlcache.EvictionReason, item *ttlcache.Item[string, Template]) {
		peers.Purge(item.Key())

		template := item.Value()

		err := template.Close(ctx)
		if err != nil {
			logger.L().Warn(ctx, "failed to cleanup template data", zap.String("item_key", item.Key()), zap.Error(err))
		}
	})

	// Delete the old build cache directory content.
	err := cleanDir(config.DefaultCacheDir)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("failed to remove old build cache directory: %w", err)
	}

	buildStore, err := build.NewDiffStore(
		config,
		flags,
		config.DefaultCacheDir,
		buildCacheTTL,
		buildCacheDelayEviction,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create build store: %w", err)
	}

	return &Cache{
		blockMetrics:  metrics,
		config:        config,
		persistence:   persistence,
		buildStore:    buildStore,
		cache:         cache,
		flags:         flags,
		rootCachePath: config.BuilderConfig.SharedChunkCacheDir,
		peers:         peers,
	}, nil
}

func (c *Cache) Start(ctx context.Context) {
	c.buildStore.Start(ctx)

	go c.cache.Start()

	if c.config.TemplateCacheMinFreeMemoryMB > 0 {
		go c.startMemoryPressureEviction(ctx)
	}
}

// memAvailableBytes reads MemAvailable from /proc/meminfo.
// This is the correct metric for "how much memory can new allocations use"
// because it includes reclaimable page cache, unlike Sysinfo.Freeram which
// only counts completely unused pages and is typically very low.
func memAvailableBytes() (uint64, error) {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, fmt.Errorf("read /proc/meminfo: %w", err)
	}

	for _, line := range bytes.Split(data, []byte("\n")) {
		if !bytes.HasPrefix(line, []byte("MemAvailable:")) {
			continue
		}
		// Format: "MemAvailable:   12345678 kB"
		fields := bytes.Fields(line)
		if len(fields) < 2 {
			break
		}

		kb, err := strconv.ParseUint(string(fields[1]), 10, 64)
		if err != nil {
			return 0, fmt.Errorf("parse MemAvailable: %w", err)
		}

		return kb * 1024, nil
	}

	return 0, fmt.Errorf("MemAvailable not found in /proc/meminfo")
}

// startMemoryPressureEviction evicts one LRU template cache entry per tick
// whenever MemAvailable on the host drops below TemplateCacheMinFreeMemoryMB.
// One entry per tick is intentional: mmap page reclamation is not instantaneous,
// so the OS memory stats won't reflect the freed pages until after the next tick,
// preventing the loop from over-evicting the entire cache in a single burst.
func (c *Cache) startMemoryPressureEviction(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	thresholdBytes := uint64(c.config.TemplateCacheMinFreeMemoryMB) * 1024 * 1024

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			available, err := memAvailableBytes()
			if err != nil {
				logger.L().Warn(ctx, "memory pressure eviction: failed to read MemAvailable", zap.Error(err))
				continue
			}

			if available >= thresholdBytes {
				continue
			}

			// Find the LRU entry: the one whose TTL was reset least recently,
			// i.e. the item with the earliest ExpiresAt timestamp.
			items := c.cache.Items()
			if len(items) == 0 {
				continue
			}

			var (
				oldestKey    string
				oldestExpiry time.Time
			)
			for k, item := range items {
				exp := item.ExpiresAt()
				if oldestKey == "" || exp.Before(oldestExpiry) {
					oldestKey = k
					oldestExpiry = exp
				}
			}

			logger.L().Info(ctx, "memory pressure eviction: evicting LRU template",
				zap.String("key", oldestKey),
				zap.Uint64("mem_available_bytes", available),
				zap.Uint64("threshold_bytes", thresholdBytes),
			)
			c.cache.Delete(oldestKey)
		}
	}
}

func (c *Cache) Stop() {
	c.buildStore.Close()
	c.cache.Stop()
	c.peers.Close()
}

func (c *Cache) Items() map[string]*ttlcache.Item[string, Template] {
	return c.cache.Items()
}

// LookupDiff returns the locally-cached diff for the given build and file name.
// Returns (nil, false) if the diff is not cached locally.
func (c *Cache) LookupDiff(buildID string, diffType build.DiffType) (build.Diff, bool) {
	key := build.GetDiffStoreKey(buildID, diffType)

	return c.buildStore.Lookup(key)
}

// Invalidate removes a template from the cache, forcing a refetch on next access.
func (c *Cache) Invalidate(buildID string) {
	c.cache.Delete(buildID)
}

// InvalidateAll clears all cached templates and build diffs.
// Used for cold start benchmarks to ensure no cached data is reused.
func (c *Cache) InvalidateAll() {
	c.cache.DeleteAll()
	c.buildStore.RemoveCache()
}

// GetTemplateOpts configures optional behavior for GetTemplate.
type GetTemplateOpts struct {
	MaxSandboxLengthHours int64
}

func (c *Cache) GetTemplate(
	ctx context.Context,
	buildID string,
	isSnapshot bool,
	isBuilding bool,
	opts ...GetTemplateOpts,
) (Template, error) {
	ctx, span := tracer.Start(ctx, "get template", trace.WithAttributes(
		attribute.Bool("is_snapshot", isSnapshot),
		attribute.Bool("is_building", isBuilding),
	))
	defer span.End()

	persistence := c.persistence
	// Because of the template caching, if we enable the NFS cache feature flag,
	// it will start working only for new orchestrators or new builds.
	if path, enabled := c.useNFSCache(ctx, isBuilding, isSnapshot); enabled {
		logger.L().Info(ctx, "using local template cache", zap.String("path", c.rootCachePath))
		persistence = storage.WrapInNFSCache(ctx, path, persistence, c.flags)
		span.SetAttributes(attribute.Bool("use_cache", true))
	} else {
		span.SetAttributes(attribute.Bool("use_cache", false))
	}

	// Wrap persistence with per-buildID peer routing.
	// Each layer's buildID is checked against Redis to find the source orchestrator.
	// This allows pulling data directly from the peer before GCS upload completes.
	if c.flags.BoolFlag(ctx, featureflags.PeerToPeerChunkTransferFlag) {
		persistence = peerclient.NewRoutingProvider(persistence, c.peers)
	}

	storageTemplate, err := newTemplateFromStorage(
		c.config.BuilderConfig,
		buildID,
		resolvedHeader(nil),
		resolvedHeader(nil),
		persistence,
		c.blockMetrics,
		nil,
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create template cache from storage: %w", err)
	}

	var maxLen int64
	if len(opts) > 0 {
		maxLen = opts[0].MaxSandboxLengthHours
	}

	return c.getTemplateWithFetch(ctx, storageTemplate, maxLen), nil
}

func resolvedHeader(h *header.Header) *utils.SetOnce[*header.Header] {
	s := utils.NewSetOnce[*header.Header]()
	_ = s.SetValue(h)

	return s
}

func (c *Cache) AddSnapshot(
	ctx context.Context,
	buildId string,
	memfileHeader *utils.SetOnce[*header.Header],
	rootfsHeader *utils.SetOnce[*header.Header],
	localSnapfile File,
	localMetafile File,
	memfileDiff build.Diff,
	rootfsDiff build.Diff,
) error {
	switch memfileDiff.(type) {
	case *build.NoDiff:
	default:
		c.buildStore.Add(memfileDiff)
	}

	switch rootfsDiff.(type) {
	case *build.NoDiff:
	default:
		c.buildStore.Add(rootfsDiff)
	}

	storageTemplate, err := newTemplateFromStorage(
		c.config.BuilderConfig,
		buildId,
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

	c.getTemplateWithFetch(ctx, storageTemplate, 0)

	return nil
}

// GetCachedTemplate returns the template for buildID if it is currently in the cache.
func (c *Cache) GetCachedTemplate(buildID string) (Template, bool) {
	item := c.cache.Get(buildID)
	if item == nil {
		return nil, false
	}

	return item.Value(), true
}

// UpdateMetadata overwrites the local metadata file for a cached template so that
// subsequent calls to Template.Metadata() on this node return the updated data
// (e.g. with freshly computed prefetch mappings) without requiring a cache
// invalidation or GCS round-trip.
func (c *Cache) UpdateMetadata(buildID string, meta metadata.Template) error {
	t, ok := c.GetCachedTemplate(buildID)
	if !ok {
		return fmt.Errorf("template %q not in cache", buildID)
	}

	return t.UpdateMetadata(meta)
}

func (c *Cache) useNFSCache(ctx context.Context, isBuilding bool, isSnapshot bool) (string, bool) {
	if isBuilding {
		// caching this layer doesn't speed up the next sandbox launch,
		// as the previous template isn't used to load the one that's being built.
		return "", false
	}

	var flagName featureflags.BoolFlag
	if isSnapshot {
		flagName = featureflags.SnapshotFeatureFlag
	} else {
		flagName = featureflags.TemplateFeatureFlag
	}

	useNFSCache := c.flags.BoolFlag(ctx, flagName)
	if useNFSCache {
		if c.rootCachePath == "" {
			logger.L().Warn(ctx, "NFSCache feature flag is enabled but cache path is not set")

			return "", false
		}
	}

	return c.rootCachePath, useNFSCache
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

func (c *Cache) getTemplateWithFetch(ctx context.Context, tmpl *storageTemplate, maxSandboxLengthHours int64) Template {
	ttl := templateExpiration
	if maxSandboxLengthHours > 0 {
		ttl = max(ttl, time.Duration(maxSandboxLengthHours)*time.Hour+templateExpirationBuffer)
	}

	key := tmpl.Files().CacheKey()

	c.extendMu.Lock()
	t, found := c.cache.GetOrSet(key, tmpl, ttlcache.WithTTL[string, Template](ttl))
	if found && t.TTL() < ttl {
		// Another team with a shorter max length cached this entry; extend it.
		c.cache.Set(key, t.Value(), ttl)
	}
	c.extendMu.Unlock()

	if !found {
		missesMetric.Add(ctx, 1)
		// We don't want to cancel the request if the request was canceled, because it can be used by other templates
		// It's a little bit problematic, because shutdown won't cancel the fetch
		go tmpl.Fetch(context.WithoutCancel(ctx), c.buildStore)
	} else {
		hitsMetric.Add(ctx, 1)
	}

	return t.Value()
}
