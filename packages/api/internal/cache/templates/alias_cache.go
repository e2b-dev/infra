package templatecache

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	sqlcdb "github.com/e2b-dev/infra/packages/db/client"
	"github.com/e2b-dev/infra/packages/db/pkg/dberrors"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/cache"
	"github.com/e2b-dev/infra/packages/shared/pkg/id"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	redis_utils "github.com/e2b-dev/infra/packages/shared/pkg/redis"
)

const (
	aliasCacheTTL             = 5 * time.Minute
	aliasCacheRefreshInterval = time.Minute

	aliasCacheKeyPrefix     = "template:alias"
	aliasReverseIndexPrefix = "template:alias-reverse"
)

// AliasInfo holds resolved alias information
type AliasInfo struct {
	TemplateID string    `json:"template_id"`
	TeamID     uuid.UUID `json:"team_id"`
	Public     bool      `json:"public"`
	NotFound   bool      `json:"not_found"` // tombstone marker for caching negative lookups
}

var notFoundTombstone = &AliasInfo{NotFound: true}

// AliasCache resolves namespace/alias to templateID with fallback logic.
// This is the main resolution layer implementing the namespace lookup flowchart.
type AliasCache struct {
	cache *cache.RedisCache[*AliasInfo]
	db    *sqlcdb.Client
}

func NewAliasCache(db *sqlcdb.Client, redisClient redis.UniversalClient) *AliasCache {
	rc := cache.NewRedisCache[*AliasInfo](cache.RedisConfig[*AliasInfo]{
		TTL:             aliasCacheTTL,
		RefreshInterval: aliasCacheRefreshInterval,
		RedisClient:     redisClient,
		RedisPrefix:     aliasCacheKeyPrefix,
	})

	return &AliasCache{
		cache: rc,
		db:    db,
	}
}

func buildAliasKey(namespace *string, alias string) string {
	if namespace == nil {
		return alias
	}

	return id.WithNamespace(*namespace, alias)
}

// Resolve implements the namespace resolution flowchart:
//   - Explicit namespace: lookup with namespace directly, no fallback
//   - Bare alias: try namespaceFallback first, then NULL (promoted templates)
func (c *AliasCache) Resolve(ctx context.Context, identifier string, namespaceFallback string) (*AliasInfo, error) {
	ctx, span := tracer.Start(ctx, "resolve alias", trace.WithAttributes(
		attribute.String("identifier", identifier),
		attribute.String("namespace_fallback", namespaceFallback),
	))
	defer span.End()

	namespace, alias := id.SplitIdentifier(identifier)

	if namespace != nil {
		// Explicit namespace - lookup directly, no fallback
		return c.lookup(ctx, namespace, alias)
	}

	// Bare alias - try fallback namespace first (team's namespace)
	info, err := c.lookup(ctx, &namespaceFallback, alias)
	if err == nil {
		return info, nil
	}

	// If not found, try NULL namespace (promoted templates)
	if errors.Is(err, ErrTemplateNotFound) {
		info, err = c.lookup(ctx, nil, alias)
		if err == nil {
			return info, nil
		}
	}

	return nil, err
}

// lookup performs a single lookup (cache then DB) for namespace/alias.
// Caches both positive hits and negative hits to avoid repeated DB queries.
func (c *AliasCache) lookup(ctx context.Context, namespace *string, alias string) (*AliasInfo, error) {
	ctx, span := tracer.Start(ctx, "lookup alias")
	defer span.End()

	key := buildAliasKey(namespace, alias)

	info, err := c.cache.GetOrSet(ctx, key, c.fetchFromDB)
	if err != nil {
		return nil, err
	}

	if info.NotFound {
		return nil, ErrTemplateNotFound
	}

	return info, nil
}

// cacheByTemplateID caches info also by template ID and tracks the alias key
// in a Redis reverse index set for bulk invalidation by template ID.
func (c *AliasCache) cacheByTemplateID(ctx context.Context, originalKey string, info *AliasInfo) {
	if info.NotFound {
		return
	}

	idKey := buildAliasKey(nil, info.TemplateID)
	if idKey != originalKey {
		c.cache.Set(ctx, idKey, info)
	}

	// Track alias key in reverse index for InvalidateByTemplateID
	c.trackReverseKey(ctx, originalKey, idKey, info)
}

func (c *AliasCache) trackReverseKey(ctx context.Context, originalKey string, idKey string, info *AliasInfo) {
	rc := c.cache.RedisClient()
	reverseKey := redis_utils.CreateKey(aliasReverseIndexPrefix, info.TemplateID)

	ctx, cancel := context.WithTimeout(ctx, cache.RedisDefaultTimeout)
	defer cancel()

	pipe := rc.Pipeline()
	pipe.SAdd(ctx, reverseKey, originalKey, idKey)
	pipe.Expire(ctx, reverseKey, aliasCacheTTL)
	_, err := pipe.Exec(ctx)
	if err != nil {
		logger.L().Warn(ctx, "failed to cache alias by template ID", zap.Error(err))
	}
}

func (c *AliasCache) fetchFromDB(ctx context.Context, key string) (info *AliasInfo, err error) {
	ctx, span := tracer.Start(ctx, "fetch alias from DB", trace.WithAttributes(
		attribute.String("key", key),
	))
	defer span.End()

	// Also cache by template ID for direct ID lookups (use nil namespace since
	// direct ID lookups don't have namespace context)
	defer func() {
		if err == nil {
			c.cacheByTemplateID(ctx, key, info)
		}
	}()

	namespace, alias := id.SplitIdentifier(key)

	// Try alias lookup first
	result, err := c.db.GetTemplateByAlias(ctx, queries.GetTemplateByAliasParams{
		Alias:     alias,
		Namespace: namespace,
	})
	if err == nil {
		return &AliasInfo{
			TemplateID: result.ID,
			TeamID:     result.TeamID,
			Public:     result.Public,
		}, nil
	}

	// If alias not found and no explicit namespace, try direct ID lookup.
	// ID fallback is only allowed for bare aliases (namespace == nil) because:
	// - "team-x/<templateID>" should fail if no alias exists in that namespace
	// - "<templateID>" (bare) should succeed via ID lookup after alias lookups fail
	if dberrors.IsNotFoundError(err) {
		if namespace == nil {
			idResult, idErr := c.db.GetTemplateById(ctx, alias)
			if idErr == nil {
				return &AliasInfo{
					TemplateID: idResult.ID,
					TeamID:     idResult.TeamID,
					Public:     idResult.Public,
				}, nil
			}

			if !dberrors.IsNotFoundError(idErr) {
				return nil, fmt.Errorf("fetching template by ID: %w", idErr)
			}
		}

		return notFoundTombstone, nil
	}

	return nil, fmt.Errorf("fetching template by alias: %w", err)
}

// LookupByID looks up template info by direct template ID only (no alias resolution).
// Uses the same cache as alias lookups since we cache by template ID too.
func (c *AliasCache) LookupByID(ctx context.Context, templateID string) (*AliasInfo, error) {
	return c.lookup(ctx, nil, templateID)
}

func (c *AliasCache) Invalidate(ctx context.Context, namespace *string, alias string) {
	c.cache.Delete(ctx, buildAliasKey(namespace, alias))
}

// popReverseIndexScript atomically reads all members from the reverse index set
// and deletes the set. Both operations target the same key (same hash slot).
// Returns the list of alias cache keys that were in the set.
// KEYS[1] = reverse index set key
var popReverseIndexScript = redis.NewScript(`
	local members = redis.call("SMEMBERS", KEYS[1])
	redis.call("DEL", KEYS[1])
	return members
`)

// InvalidateByTemplateID removes all cache entries pointing to the given template ID.
// It atomically pops the reverse index set (Lua script, single slot), then pipelines
// DELs for the alias cache keys (which may span different slots).
func (c *AliasCache) InvalidateByTemplateID(ctx context.Context, templateID string) {
	rc := c.cache.RedisClient()
	reverseKey := redis_utils.CreateKey(aliasReverseIndexPrefix, templateID)

	ctx, cancel := context.WithTimeout(ctx, cache.RedisDefaultTimeout)
	defer cancel()

	members, err := popReverseIndexScript.Run(ctx, rc, []string{reverseKey}).StringSlice()
	if err != nil {
		if !errors.Is(err, redis.Nil) {
			logger.L().Warn(ctx, "failed to pop alias reverse index",
				zap.String("template_id", templateID),
				zap.Error(err),
			)
		}
	}

	if len(members) == 0 {
		return
	}

	pipe := rc.Pipeline()
	for _, aliasKey := range members {
		pipe.Del(ctx, c.cache.RedisKey(aliasKey))
	}

	if _, err := pipe.Exec(ctx); err != nil {
		logger.L().Warn(ctx, "failed to delete alias cache entries",
			zap.String("template_id", templateID),
			zap.Error(err),
		)
	}
}

func (c *AliasCache) Close(ctx context.Context) error {
	return c.cache.Close(ctx)
}
