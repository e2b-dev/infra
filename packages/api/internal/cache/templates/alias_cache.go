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

// AliasInfo holds resolved alias information (immutable mapping data only).
// Mutable metadata like Public is in TemplateMetadata.
type AliasInfo struct {
	TemplateID       string    `json:"template_id"`
	TeamID           uuid.UUID `json:"team_id"`
	MatchedNamespace string    `json:"matched_namespace,omitempty"` // namespace under which the alias matched ("" = NULL/promoted or ID hit)
	NotFound         bool      `json:"not_found"`                   // tombstone marker for caching negative lookups
}

// FullName returns the namespaced canonical name under which this info was
// resolved (e.g. "team-x/myalias"), falling back to identifier when the alias
// matched under NULL namespace or came from a direct templateID lookup.
func (a *AliasInfo) FullName(identifier string) string {
	if a == nil || a.MatchedNamespace == "" {
		return identifier
	}
	if ns, _ := id.SplitIdentifier(identifier); ns != nil {
		return identifier
	}

	return id.WithNamespace(a.MatchedNamespace, identifier)
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

// cacheByTemplateID caches info also by template ID for direct ID lookups.
func (c *AliasCache) cacheByTemplateID(ctx context.Context, originalKey string, info *AliasInfo) {
	if info.NotFound {
		return
	}

	idKey := buildAliasKey(nil, info.TemplateID)
	if idKey == originalKey {
		return
	}

	// Clear MatchedNamespace — direct-ID entries must not surface another
	// team's slug to later bare-ID lookups.
	idInfo := *info
	idInfo.MatchedNamespace = ""
	c.cache.Set(ctx, idKey, &idInfo)
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

	var matchedNamespace string
	if namespace != nil {
		matchedNamespace = *namespace
	}

	// Try alias lookup first
	result, err := c.db.GetTemplateByAlias(ctx, queries.GetTemplateByAliasParams{
		Alias:     alias,
		Namespace: namespace,
	})
	if err == nil {
		return &AliasInfo{
			TemplateID:       result.ID,
			TeamID:           result.TeamID,
			MatchedNamespace: matchedNamespace,
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

// InvalidateAliasesByTemplateID deletes alias cache entries for the given keys
// plus the template-ID-keyed entry. aliasKeys should be cache-key-formatted
// (e.g. "namespace/alias" or bare "alias"), as returned by DeleteTemplate.
func (c *AliasCache) InvalidateAliasesByTemplateID(ctx context.Context, templateID string, aliasKeys []string) {
	for _, key := range aliasKeys {
		c.cache.Delete(ctx, key)
	}

	// Also delete the template-ID-keyed entry
	c.cache.Delete(ctx, templateID)
}

func (c *AliasCache) Close(ctx context.Context) error {
	return c.cache.Close(ctx)
}
