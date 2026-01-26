package templatecache

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	sqlcdb "github.com/e2b-dev/infra/packages/db/client"
	"github.com/e2b-dev/infra/packages/db/dberrors"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/cache"
	"github.com/e2b-dev/infra/packages/shared/pkg/id"
)

// AliasInfo holds resolved alias information
type AliasInfo struct {
	TemplateID string
	TeamID     uuid.UUID
	Public     bool
}

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
func (c *AliasCache) Resolve(ctx context.Context, identifier string, namespaceFallback string) (*AliasInfo, *api.APIError) {
	ctx, span := tracer.Start(ctx, "resolve alias")
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
	if err.Code == http.StatusNotFound {
		info, err = c.lookup(ctx, nil, alias)
		if err == nil {
			return info, nil
		}
	}

	return nil, err
}

// lookup performs a single lookup (cache then DB) for namespace/alias
func (c *AliasCache) lookup(ctx context.Context, namespace *string, alias string) (*AliasInfo, *api.APIError) {
	key := buildAliasKey(namespace, alias)

	info, err := c.cache.GetOrSet(ctx, key, c.fetchFromDB)
	if err != nil {
		var apiErr *api.APIError
		if errors.As(err, &apiErr) {
			return nil, apiErr
		}

		return nil, &api.APIError{
			Code:      http.StatusInternalServerError,
			ClientMsg: fmt.Sprintf("error resolving template: %v", err),
			Err:       err,
		}
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
			idResult, err := c.db.GetTemplateById(ctx, alias)
			if err == nil {
				return &AliasInfo{
					TemplateID: idResult.ID,
					TeamID:     idResult.TeamID,
					Public:     idResult.Public,
				}, nil
			}

			if !dberrors.IsNotFoundError(err) {
				return nil, &api.APIError{
					Code:      http.StatusInternalServerError,
					ClientMsg: fmt.Sprintf("error resolving template: %v", err),
					Err:       err,
				}
			}
		}

		return nil, &api.APIError{
			Code:      http.StatusNotFound,
			ClientMsg: fmt.Sprintf("template '%s' not found", alias),
			Err:       err,
		}
	}

	// GetTemplateByAlias failed with a non-"not found" error
	return nil, &api.APIError{
		Code:      http.StatusInternalServerError,
		ClientMsg: fmt.Sprintf("error resolving template: %v", err),
		Err:       err,
	}
}

// LookupByID looks up template info by direct template ID only (no alias resolution).
// Uses the same cache as alias lookups since we cache by template ID too.
func (c *AliasCache) LookupByID(ctx context.Context, templateID string) (*AliasInfo, *api.APIError) {
	return c.lookup(ctx, nil, templateID)
}

func (c *AliasCache) Invalidate(namespace *string, alias string) {
	key := buildAliasKey(namespace, alias)
	c.cache.Delete(key)
}

func (c *AliasCache) Close(ctx context.Context) error {
	return c.cache.Close(ctx)
}
