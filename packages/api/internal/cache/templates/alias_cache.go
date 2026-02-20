package templatecache

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	sqlcdb "github.com/e2b-dev/infra/packages/db/client"
	"github.com/e2b-dev/infra/packages/db/pkg/dberrors"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/cache"
	"github.com/e2b-dev/infra/packages/shared/pkg/id"
)

const (
	aliasCacheTTL             = 5 * time.Minute
	aliasCacheRefreshInterval = time.Minute

	aliasCacheKeyPrefix = "template:alias"
)

// AliasResult holds the minimal alias→templateID mapping cached per alias key.
type AliasResult struct {
	TemplateID string `json:"template_id"`
	NotFound   bool   `json:"not_found"` // tombstone marker for caching negative lookups
}

var notFoundTombstone = &AliasResult{NotFound: true}

// AliasCache resolves namespace/alias to templateID with fallback logic.
// This is the main resolution layer implementing the namespace lookup flowchart.
type AliasCache struct {
	cache *cache.RedisCache[*AliasResult]
	db    *sqlcdb.Client
}

func NewAliasCache(db *sqlcdb.Client, redisClient redis.UniversalClient) *AliasCache {
	rc := cache.NewRedisCache[*AliasResult](cache.RedisConfig[*AliasResult]{
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
func (c *AliasCache) Resolve(ctx context.Context, identifier string, namespaceFallback string) (string, error) {
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
	templateID, err := c.lookup(ctx, &namespaceFallback, alias)
	if err == nil {
		return templateID, nil
	}

	// If not found, try NULL namespace (promoted templates)
	if errors.Is(err, ErrTemplateNotFound) {
		templateID, err = c.lookup(ctx, nil, alias)
		if err == nil {
			return templateID, nil
		}
	}

	return "", err
}

// lookup performs a single lookup (cache then DB) for namespace/alias.
// Caches both positive hits and negative hits to avoid repeated DB queries.
func (c *AliasCache) lookup(ctx context.Context, namespace *string, alias string) (string, error) {
	ctx, span := tracer.Start(ctx, "lookup alias")
	defer span.End()

	key := buildAliasKey(namespace, alias)

	result, err := c.cache.GetOrSet(ctx, key, c.fetchFromDB)
	if err != nil {
		return "", err
	}

	if result.NotFound {
		return "", ErrTemplateNotFound
	}

	return result.TemplateID, nil
}

func (c *AliasCache) fetchFromDB(ctx context.Context, key string) (result *AliasResult, err error) {
	ctx, span := tracer.Start(ctx, "fetch alias from DB", trace.WithAttributes(
		attribute.String("key", key),
	))
	defer span.End()

	namespace, alias := id.SplitIdentifier(key)

	// Try alias lookup first
	aliasRow, err := c.db.GetTemplateByAlias(ctx, queries.GetTemplateByAliasParams{
		Alias:     alias,
		Namespace: namespace,
	})
	if err == nil {
		return &AliasResult{
			TemplateID: aliasRow.ID,
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
				return &AliasResult{
					TemplateID: idResult.ID,
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

// LookupByID looks up a template by direct template ID only (no alias resolution).
func (c *AliasCache) LookupByID(ctx context.Context, templateID string) (string, error) {
	return c.lookup(ctx, nil, templateID)
}

func (c *AliasCache) Invalidate(ctx context.Context, namespace *string, alias string) {
	c.cache.Delete(ctx, buildAliasKey(namespace, alias))
}

func (c *AliasCache) Close(ctx context.Context) error {
	return c.cache.Close(ctx)
}
