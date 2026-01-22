package templatecache

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	sqlcdb "github.com/e2b-dev/infra/packages/db/client"
	"github.com/e2b-dev/infra/packages/db/dberrors"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/cache"
	"github.com/e2b-dev/infra/packages/shared/pkg/id"
	sharedUtils "github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

const (
	templateInfoExpiration = 5 * time.Minute
	refreshInterval        = 1 * time.Minute
	refreshTimeout         = 30 * time.Second
)

var tracer = otel.Tracer("github.com/e2b-dev/infra/packages/api/internal/cache/templates")

func buildCacheKey(templateID, tag string) string {
	return templateID + id.TagSeparator + tag
}

// TemplateInfo holds cached template with build information
type TemplateInfo struct {
	Template  *api.Template
	TeamID    uuid.UUID
	ClusterID uuid.UUID
	Build     *queries.EnvBuild
	Tag       string
}

// TemplateCache caches template+build by templateID:tag.
// This is a simple lookup layer - resolution happens in AliasCache.
type TemplateCache struct {
	cache      *cache.Cache[string, *TemplateInfo]
	db         *sqlcdb.Client
	aliasCache *AliasCache
}

func NewTemplateCache(db *sqlcdb.Client) *TemplateCache {
	config := cache.Config[string, *TemplateInfo]{
		TTL:             templateInfoExpiration,
		RefreshInterval: refreshInterval,
		RefreshTimeout:  refreshTimeout,
		ExtractKeyFunc: func(value *TemplateInfo) string {
			return buildCacheKey(value.Template.TemplateID, value.Tag)
		},
	}

	return &TemplateCache{
		cache:      cache.NewCache(config),
		db:         db,
		aliasCache: NewAliasCache(db),
	}
}

// ResolveAlias resolves an identifier to AliasInfo (templateID, teamID, public).
// The identifier is "namespace/alias" or just "alias" (already validated by id.ParseName).
// namespaceFallback is used for bare aliases (no explicit namespace).
func (c *TemplateCache) ResolveAlias(ctx context.Context, identifier string, namespaceFallback string) (*AliasInfo, *api.APIError) {
	return c.aliasCache.Resolve(ctx, identifier, namespaceFallback)
}

// GetByID looks up template info by direct template ID only (no alias resolution).
func (c *TemplateCache) GetByID(ctx context.Context, templateID string) (*AliasInfo, *api.APIError) {
	return c.aliasCache.LookupByID(ctx, templateID)
}

// Get fetches a template with build by templateID and tag.
// Does NOT do alias resolution - callers should use ResolveAlias first.
// Performs access control and cluster checks.
func (c *TemplateCache) Get(ctx context.Context, templateID string, tag *string, teamID uuid.UUID, clusterID uuid.UUID) (*api.Template, *queries.EnvBuild, *api.APIError) {
	ctx, span := tracer.Start(ctx, "get template")
	defer span.End()

	// Step 1: Get template with build by ID and tag
	templateInfo, apiErr := c.getByID(ctx, templateID, tag)
	if apiErr != nil {
		return nil, nil, apiErr
	}

	// Step 2: Access control check
	if templateInfo.TeamID != teamID && !templateInfo.Template.Public {
		return nil, nil, &api.APIError{
			Code:      http.StatusForbidden,
			ClientMsg: fmt.Sprintf("Team '%s' does not have access to the template '%s'", teamID, templateID),
			Err:       fmt.Errorf("team '%s' does not have access to the template '%s'", teamID, templateID),
		}
	}

	// Step 3: Cluster check
	if templateInfo.ClusterID != clusterID {
		return nil, nil, &api.APIError{
			Code:      http.StatusBadRequest,
			ClientMsg: fmt.Sprintf("Template '%s' is not available in requested cluster", templateID),
			Err:       fmt.Errorf("template '%s' is not available in requested cluster '%s'", templateID, clusterID),
		}
	}

	return templateInfo.Template, templateInfo.Build, nil
}

// getByID fetches template+build by templateID and tag
func (c *TemplateCache) getByID(ctx context.Context, templateID string, tag *string) (*TemplateInfo, *api.APIError) {
	tagValue := id.DefaultTag
	if tag != nil {
		tagValue = *tag
	}
	cacheKey := buildCacheKey(templateID, tagValue)

	info, err := c.cache.GetOrSet(ctx, cacheKey, func(ctx context.Context, _ string) (*TemplateInfo, error) {
		return c.fetchTemplateWithBuild(ctx, templateID, tag)
	})
	if err != nil {
		var apiErr *api.APIError
		if errors.As(err, &apiErr) {
			return nil, apiErr
		}

		return nil, &api.APIError{
			Code:      http.StatusInternalServerError,
			ClientMsg: fmt.Sprintf("error fetching template: %v", err),
			Err:       err,
		}
	}

	return info, nil
}

func (c *TemplateCache) fetchTemplateWithBuild(ctx context.Context, templateID string, tag *string) (*TemplateInfo, error) {
	ctx, span := tracer.Start(ctx, "fetch template with build")
	defer span.End()

	result, err := c.db.GetTemplateWithBuildByTag(ctx, queries.GetTemplateWithBuildByTagParams{
		TemplateID: templateID,
		Tag:        tag,
	})
	if err != nil {
		if dberrors.IsNotFoundError(err) {
			tagMsg := ""
			if tag != nil {
				tagMsg = fmt.Sprintf(" with tag '%s'", *tag)
			}

			return nil, &api.APIError{
				Code:      http.StatusNotFound,
				ClientMsg: fmt.Sprintf("template '%s'%s not found", templateID, tagMsg),
				Err:       err,
			}
		}

		return nil, &api.APIError{
			Code:      http.StatusInternalServerError,
			ClientMsg: fmt.Sprintf("error fetching template: %v", err),
			Err:       err,
		}
	}

	build := &result.EnvBuild
	template := result.Env
	clusterID := utils.WithClusterFallback(template.ClusterID)

	tagValue := sharedUtils.DerefOrDefault(tag, id.DefaultTag)

	return &TemplateInfo{
		Template: &api.Template{
			TemplateID: template.ID,
			BuildID:    build.ID.String(),
			Public:     template.Public,
			Aliases:    result.Aliases,
		},
		TeamID:    template.TeamID,
		ClusterID: clusterID,
		Build:     build,
		Tag:       tagValue,
	}, nil
}

func (c *TemplateCache) Invalidate(templateID string, tag *string) {
	tagValue := id.DefaultTag
	if tag != nil {
		tagValue = *tag
	}
	c.cache.Delete(buildCacheKey(templateID, tagValue))
}

// InvalidateAllTags invalidates the cache for the given templateID across all tags
func (c *TemplateCache) InvalidateAllTags(templateID string) []string {
	keys := make([]string, 0)
	pattern := buildCacheKey(templateID, "")

	for _, key := range c.cache.Keys() {
		if strings.HasPrefix(key, pattern) {
			keys = append(keys, key)
			c.cache.Delete(key)
		}
	}

	return keys
}

// InvalidateAlias invalidates the alias cache entry
func (c *TemplateCache) InvalidateAlias(namespace *string, alias string) {
	c.aliasCache.Invalidate(namespace, alias)
}

func (c *TemplateCache) Close(ctx context.Context) error {
	return errors.Join(c.aliasCache.Close(ctx), c.cache.Close(ctx))
}
