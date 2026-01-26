package templatecache

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jellydator/ttlcache/v3"
	"go.opentelemetry.io/otel"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	sqlcdb "github.com/e2b-dev/infra/packages/db/client"
	"github.com/e2b-dev/infra/packages/db/pkg/dberrors"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/cache"
	"github.com/e2b-dev/infra/packages/shared/pkg/id"
)

const (
	templateInfoExpiration = 5 * time.Minute
	refreshInterval        = 1 * time.Minute
	refreshTimeout         = 30 * time.Second
)

var tracer = otel.Tracer("github.com/e2b-dev/infra/packages/api/internal/cache/templates")

type TemplateInfo struct {
	template  *api.Template
	teamID    uuid.UUID
	clusterID uuid.UUID
	build     *queries.EnvBuild
	tag       *string
}

type AliasCache struct {
	cache *ttlcache.Cache[string, string]
}

func NewAliasCache() *AliasCache {
	cache := ttlcache.New(ttlcache.WithTTL[string, string](templateInfoExpiration))

	go cache.Start()

	return &AliasCache{
		cache: cache,
	}
}

func (c *AliasCache) Get(alias string) (templateID string, found bool) {
	item := c.cache.Get(alias)

	if item == nil {
		return "", false
	}

	return item.Value(), true
}

func (c *AliasCache) Set(key string, value string) {
	c.cache.Set(key, value, ttlcache.DefaultTTL)
}

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
		// With this we can use alias for getting template info without having it as a key in the cache
		ExtractKeyFunc: func(value *TemplateInfo) string {
			return buildCacheKey(value.template.TemplateID, value.tag)
		},
	}
	aliasCache := NewAliasCache()

	return &TemplateCache{
		cache:      cache.NewCache(config),
		db:         db,
		aliasCache: aliasCache,
	}
}

func buildCacheKey(templateID string, tag *string) string {
	return id.NameWithTag(templateID, tag)
}

func (c *TemplateCache) Get(ctx context.Context, aliasOrEnvID string, tag *string, teamID uuid.UUID, clusterID uuid.UUID, public bool) (*api.Template, *queries.EnvBuild, *api.APIError) {
	ctx, span := tracer.Start(ctx, "get template")
	defer span.End()

	// Resolve alias to template ID if needed
	templateID, found := c.aliasCache.Get(aliasOrEnvID)
	if !found {
		templateID = aliasOrEnvID
	}

	cacheKey := buildCacheKey(templateID, tag)

	// Fetch or get from cache with automatic refresh
	templateInfo, err := c.cache.GetOrSet(ctx, cacheKey, c.fetchTemplateInfo)
	if err != nil {
		var apiErr *api.APIError
		if errors.As(err, &apiErr) {
			return nil, nil, apiErr
		}

		return nil, nil, &api.APIError{Code: http.StatusInternalServerError, ClientMsg: fmt.Sprintf("error while getting template: %v", err), Err: err}
	}

	// Validate access control
	if templateInfo.teamID != teamID && (!public || !templateInfo.template.Public) {
		return nil, nil, &api.APIError{Code: http.StatusForbidden, ClientMsg: fmt.Sprintf("Team '%s' does not have access to the template '%s'", teamID, aliasOrEnvID), Err: fmt.Errorf("team '%s' does not have access to the template '%s'", teamID, aliasOrEnvID)}
	}

	if templateInfo.clusterID != clusterID {
		return nil, nil, &api.APIError{Code: http.StatusBadRequest, ClientMsg: fmt.Sprintf("Template '%s' is not available in requested cluster", aliasOrEnvID), Err: fmt.Errorf("template '%s' is not available in requested cluster '%s'", aliasOrEnvID, clusterID)}
	}

	return templateInfo.template, templateInfo.build, nil
}

// fetchTemplateInfo fetches template info from the database
func (c *TemplateCache) fetchTemplateInfo(ctx context.Context, cacheKey string) (*TemplateInfo, error) {
	ctx, span := tracer.Start(ctx, "fetch template info")
	defer span.End()

	aliasOrEnvID, tag, err := id.ParseTemplateIDOrAliasWithTag(cacheKey)
	if err != nil {
		return nil, &api.APIError{Code: http.StatusBadRequest, ClientMsg: fmt.Sprintf("invalid template ID: %s", err), Err: err}
	}

	result, err := c.db.GetTemplateWithBuildByTag(ctx, queries.GetTemplateWithBuildByTagParams{
		AliasOrEnvID: aliasOrEnvID,
		Tag:          tag,
	})
	if err != nil {
		if dberrors.IsNotFoundError(err) {
			tagMsg := ""
			if tag != nil {
				tagMsg = fmt.Sprintf(" with tag '%s'", *tag)
			}

			return nil, &api.APIError{Code: http.StatusNotFound, ClientMsg: fmt.Sprintf("template '%s'%s not found", aliasOrEnvID, tagMsg), Err: err}
		}

		return nil, &api.APIError{Code: http.StatusInternalServerError, ClientMsg: fmt.Sprintf("error while getting template: %v", err), Err: err}
	}

	build := &result.EnvBuild
	template := result.Env
	clusterID := utils.WithClusterFallback(template.ClusterID)

	// Update alias cache (without tag, as aliases map to template IDs)
	c.aliasCache.Set(template.ID, template.ID)
	for _, alias := range result.Aliases {
		c.aliasCache.Set(alias, template.ID)
	}

	return &TemplateInfo{
		template: &api.Template{
			TemplateID: template.ID,
			BuildID:    build.ID.String(),
			Public:     template.Public,
			Aliases:    result.Aliases,
		},
		teamID:    template.TeamID,
		clusterID: clusterID,
		build:     build,
		tag:       tag,
	}, nil
}

func (c *TemplateCache) Invalidate(templateID string, tag *string) {
	c.cache.Delete(buildCacheKey(templateID, tag))
}

// Invalidate invalidates the cache for the given templateID across all tags
func (c *TemplateCache) InvalidateAllTags(templateID string) []string {
	keys := make([]string, 0)

	templateIDKey := templateID + id.TagSeparator

	for _, key := range c.cache.Keys() {
		if strings.HasPrefix(key, templateIDKey) {
			keys = append(keys, key)
			c.cache.Delete(key)
		}
	}

	return keys
}

func (c *TemplateCache) Close(ctx context.Context) error {
	c.aliasCache.cache.Stop()

	return c.cache.Close(ctx)
}
