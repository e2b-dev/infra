package templatecache

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	sqlcdb "github.com/e2b-dev/infra/packages/db/client"
	"github.com/e2b-dev/infra/packages/db/pkg/dberrors"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/cache"
	"github.com/e2b-dev/infra/packages/shared/pkg/id"
)

// AliasInfo holds resolved alias information
type AliasInfo struct {
	TemplateID string
	TeamID     uuid.UUID
	Public     bool
	notFound   bool // tombstone marker for caching negative lookups
}

var notFoundTombstone = &AliasInfo{notFound: true}

// AliasCache resolves namespace/alias to templateID with fallback logic.
// This is the main resolution layer implementing the namespace lookup flowchart.
type AliasCache struct {
	cache *cache.Cache[string, *AliasInfo]
	db    *sqlcdb.Client
}

func NewAliasCache(db *sqlcdb.Client) *AliasCache {
	config := cache.Config[string, *AliasInfo]{
		TTL:             templateInfoExpiration,
		RefreshInterval: refreshInterval,
		RefreshTimeout:  refreshTimeout,
	}

	return &AliasCache{
		cache: cache.NewCache(config),
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

	if info.notFound {
		return nil, ErrTemplateNotFound
	}

	// Also cache by template ID for direct ID lookups (use nil namespace since
	// direct ID lookups don't have namespace context)
	idKey := buildAliasKey(nil, info.TemplateID)
	if idKey != key {
		c.cache.Set(idKey, info)
	}

	return info, nil
}

func (c *AliasCache) fetchFromDB(ctx context.Context, key string) (*AliasInfo, error) {
	ctx, span := tracer.Start(ctx, "fetch alias from DB", trace.WithAttributes(
		attribute.String("key", key),
	))
	defer span.End()

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

func (c *AliasCache) Invalidate(namespace *string, alias string) {
	key := buildAliasKey(namespace, alias)
	c.cache.Delete(key)
}

// InvalidateByTemplateID removes all cache entries pointing to the given template ID
func (c *AliasCache) InvalidateByTemplateID(templateID string) {
	for _, key := range c.cache.Keys() {
		if info, found := c.cache.Get(key); found && info != nil && info.TemplateID == templateID {
			c.cache.Delete(key)
		}
	}
}

func (c *AliasCache) Close(ctx context.Context) error {
	return c.cache.Close(ctx)
}
