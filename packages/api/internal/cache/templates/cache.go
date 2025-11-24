package templatecache

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jellydator/ttlcache/v3"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	sqlcdb "github.com/e2b-dev/infra/packages/db/client"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/cache"
)

const (
	templateInfoExpiration = 5 * time.Minute
	refreshInterval        = 1 * time.Minute
	refreshTimeout         = 30 * time.Second
)

type TemplateInfo struct {
	template  *api.Template
	teamID    uuid.UUID
	clusterID uuid.UUID
	build     *queries.EnvBuild
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
			return value.template.TemplateID
		},
	}
	aliasCache := NewAliasCache()

	return &TemplateCache{
		cache:      cache.NewCache[string, *TemplateInfo](config),
		db:         db,
		aliasCache: aliasCache,
	}
}

func (c *TemplateCache) Get(ctx context.Context, aliasOrEnvID string, teamID uuid.UUID, clusterID uuid.UUID, public bool) (*api.Template, *queries.EnvBuild, *api.APIError) {
	// Resolve alias to template ID if needed
	templateID, found := c.aliasCache.Get(aliasOrEnvID)
	if !found {
		templateID = aliasOrEnvID
	}

	// Fetch or get from cache with automatic refresh
	templateInfo, err := c.cache.GetOrSet(ctx, templateID, c.fetchTemplateInfo)
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
func (c *TemplateCache) fetchTemplateInfo(ctx context.Context, aliasOrEnvID string) (*TemplateInfo, error) {
	result, err := c.db.GetTemplateWithBuild(ctx, aliasOrEnvID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, &api.APIError{Code: http.StatusNotFound, ClientMsg: fmt.Sprintf("template '%s' not found", aliasOrEnvID), Err: err}
		}

		return nil, &api.APIError{Code: http.StatusInternalServerError, ClientMsg: fmt.Sprintf("error while getting template: %v", err), Err: err}
	}

	build := &result.EnvBuild
	template := result.Env
	clusterID := utils.WithClusterFallback(template.ClusterID)

	// Update alias cache
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
	}, nil
}

// Invalidate invalidates the cache for the given templateID
func (c *TemplateCache) Invalidate(templateID string) {
	c.cache.Delete(templateID)
}

func (c *TemplateCache) Close(ctx context.Context) error {
	c.aliasCache.cache.Stop()

	return c.cache.Close(ctx)
}
