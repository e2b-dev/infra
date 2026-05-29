//go:build linux

package template

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jellydator/ttlcache/v3"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/cfg"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block"
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

	// evictionSweepInterval is how often the size-based sweeper runs.
	evictionSweepInterval = time.Second * 30

	// evictionGracePeriod protects templates accessed very recently from
	// size-based eviction, covering the window between GetTemplate and a
	// sandbox entering the running set (MarkRunning). Comfortably larger than
	// sandbox start time.
	evictionGracePeriod = time.Minute * 5

	// approxBuildMapBytes is the per-entry cost used to estimate a cached
	// header's retained mapping memory. The compact in-memory Mapping is
	// ~14 bytes/entry; the estimate is intentionally coarse (it only needs to
	// rank entries and compare against a budget).
	approxBuildMapBytes = 14
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

	// activeBuildIDs reports the set of build IDs currently backing a running
	// sandbox. Size-based eviction never closes a template in this set, since a
	// running sandbox reads through its block devices. nil disables size-based
	// eviction (the safe default until the server wires it in). Stored
	// atomically: the eviction goroutine (started by Start) reads it while the
	// server may install it later via SetActiveBuildIDs.
	activeBuildIDs atomic.Pointer[func() map[string]struct{}]

	// lastAccess records when each cached build ID was last handed out, used to
	// shield just-accessed templates from eviction during sandbox startup
	// (before they appear in activeBuildIDs).
	lastAccess sync.Map // key string -> time.Time
}

// SetActiveBuildIDs installs the in-use predicate used by size-based eviction.
// Must be called before Start. The function is consulted on every sweep and
// must return the build IDs of all currently running sandboxes.
func (c *Cache) SetActiveBuildIDs(fn func() map[string]struct{}) {
	c.activeBuildIDs.Store(&fn)
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
	cache := ttlcache.New(
		ttlcache.WithTTL[string, Template](templateExpiration),
	)

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
	go c.runEviction(ctx)
}

// runEviction periodically enforces the size-based memory budget, evicting
// idle templates oldest-first. Each tick is a no-op while the budget flag is 0
// or the in-use predicate has not been installed (the predicate may be set
// after Start, so the check is per-tick rather than once up front).
func (c *Cache) runEviction(ctx context.Context) {
	ticker := time.NewTicker(evictionSweepInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.evictOverBudget(ctx)
		}
	}
}

// mappingFootprint estimates the retained header-mapping memory of a cached
// template. Only storage-backed templates carry the long-lived merged headers
// that drive cache memory; other kinds report 0.
//
// The header is read from the resolved device rather than memfileHeader/
// rootfsHeader: on the GetTemplate path those SetOnces are pre-resolved to nil
// and the real header is loaded later inside NewStorage during Fetch, so only
// the device carries it. Devices not yet resolved (fetch in flight) report 0
// via the non-blocking SetOnce result.
func mappingFootprint(t Template) int64 {
	st, ok := t.(*storageTemplate)
	if !ok {
		return 0
	}

	var bytes int64
	for _, dev := range []*utils.SetOnce[block.ReadonlyDevice]{st.memfile, st.rootfs} {
		if dev == nil {
			continue
		}
		d, err := dev.Result()
		if err != nil || d == nil {
			continue
		}
		s, ok := d.(*Storage)
		if !ok {
			continue
		}
		if hdr := s.Header(); hdr != nil {
			bytes += int64(hdr.Mapping.Len()) * approxBuildMapBytes
		}
	}

	return bytes
}

// evictOverBudget evicts idle templates (no running sandbox) oldest-first until
// the estimated retained mapping memory is under the configured budget. A
// running sandbox reads through its template's block devices, so in-use
// templates are never evicted regardless of size or age.
func (c *Cache) evictOverBudget(ctx context.Context) {
	activeFn := c.activeBuildIDs.Load()
	if activeFn == nil {
		return
	}

	budgetMiB := c.flags.IntFlag(ctx, featureflags.TemplateCacheMaxMappingMiBFlag)
	if budgetMiB <= 0 {
		return
	}
	budget := int64(budgetMiB) << 20

	items := c.cache.Items()

	// Drop last-access entries for keys no longer cached (TTL-evicted elsewhere).
	c.lastAccess.Range(func(k, _ any) bool {
		if _, ok := items[k.(string)]; !ok {
			c.lastAccess.Delete(k)
		}

		return true
	})

	entries := make([]evictionEntry, 0, len(items))
	var total int64
	for key, item := range items {
		fp := mappingFootprint(item.Value())
		total += fp

		var lastAccess time.Time
		if ts, ok := c.lastAccess.Load(key); ok {
			lastAccess = ts.(time.Time)
		}

		entries = append(entries, evictionEntry{
			key:        key,
			footprint:  fp,
			expiresAt:  item.ExpiresAt(),
			lastAccess: lastAccess,
		})
	}
	if total <= budget {
		return
	}

	victims := selectEvictions(entries, (*activeFn)(), time.Now(), budget, total)
	for _, v := range victims {
		c.cache.Delete(v) // triggers OnEviction: peer purge + template Close
		c.lastAccess.Delete(v)
		logger.L().Info(ctx, "evicted idle template over memory budget",
			zap.String("build_id", v),
			zap.Int64("budget_bytes", budget),
		)
	}
}

type evictionEntry struct {
	key        string
	footprint  int64
	expiresAt  time.Time
	lastAccess time.Time
}

// selectEvictions returns the build IDs to evict, oldest-first, until the
// running total drops to budget. An entry is eligible only when it is not in
// the active set (no running sandbox reads it) and was last accessed before the
// grace window (shielding sandboxes still starting up). Pure function: no I/O,
// so the in-use/grace correctness rules are unit-testable.
func selectEvictions(entries []evictionEntry, active map[string]struct{}, now time.Time, budget, total int64) []string {
	eligible := make([]evictionEntry, 0, len(entries))
	for _, e := range entries {
		if e.footprint == 0 {
			continue
		}
		if _, inUse := active[e.key]; inUse {
			continue
		}
		if e.lastAccess.IsZero() || now.Sub(e.lastAccess) <= evictionGracePeriod {
			continue
		}
		eligible = append(eligible, e)
	}

	// Oldest-first: smallest TTL = least-recently set or extended.
	slices.SortFunc(eligible, func(a, b evictionEntry) int {
		return a.expiresAt.Compare(b.expiresAt)
	})

	var victims []string
	for _, e := range eligible {
		if total <= budget {
			break
		}
		victims = append(victims, e.key)
		total -= e.footprint
	}

	return victims
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

	c.lastAccess.Store(buildID, time.Now())

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

	c.lastAccess.Store(key, time.Now())

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
