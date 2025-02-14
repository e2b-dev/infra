package templatecache

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jellydator/ttlcache/v3"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/shared/pkg/db"
	"github.com/e2b-dev/infra/packages/shared/pkg/models"
)

const templateInfoExpiration = 5 * time.Minute

type TemplateInfo struct {
	template *api.Template
	teamID   uuid.UUID
	build    *models.EnvBuild
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

type TemplateCache struct {
	cache      *ttlcache.Cache[string, *TemplateInfo]
	db         *db.DB
	aliasCache *AliasCache
}

func NewTemplateCache(db *db.DB) *TemplateCache {
	cache := ttlcache.New(ttlcache.WithTTL[string, *TemplateInfo](templateInfoExpiration))
	aliasCache := NewAliasCache()
	go cache.Start()

	return &TemplateCache{
		cache:      cache,
		db:         db,
		aliasCache: aliasCache,
	}
}

func (c *TemplateCache) Get(ctx context.Context, aliasOrEnvID string, teamID uuid.UUID, public bool) (env *api.Template, build *models.EnvBuild, apiErr *api.APIError) {
	var envDB *db.Template
	var item *ttlcache.Item[string, *TemplateInfo]
	var templateInfo *TemplateInfo
	var err error

	templateID, found := c.aliasCache.Get(aliasOrEnvID)
	if found == true {
		item = c.cache.Get(templateID)
	}

	if item == nil {
		envDB, build, err = c.db.GetEnv(ctx, aliasOrEnvID)
		if err != nil {
			if models.IsNotFound(err) == true {
				return nil, nil, &api.APIError{Code: http.StatusNotFound, ClientMsg: fmt.Sprintf("template '%s' not found", aliasOrEnvID), Err: err}
			}
			return nil, nil, &api.APIError{Code: http.StatusInternalServerError, ClientMsg: fmt.Sprintf("error while getting template: %v", err), Err: err}
		}

		c.aliasCache.cache.Set(envDB.TemplateID, envDB.TemplateID, templateInfoExpiration)
		if envDB.Aliases != nil {
			for _, alias := range *envDB.Aliases {
				c.aliasCache.cache.Set(alias, envDB.TemplateID, templateInfoExpiration)
			}
		}

		// Check if the team has access to the environment
		if envDB.TeamID != teamID && (!public || !envDB.Public) {
			return nil, nil, &api.APIError{Code: http.StatusForbidden, ClientMsg: fmt.Sprintf("Team  '%s' does not have access to the template '%s'", teamID, aliasOrEnvID), Err: fmt.Errorf("team  '%s' does not have access to the template '%s'", teamID, aliasOrEnvID)}
		}

		templateInfo = &TemplateInfo{template: &api.Template{
			TemplateID: envDB.TemplateID,
			BuildID:    build.ID.String(),
			Public:     envDB.Public,
			Aliases:    envDB.Aliases,
		}, teamID: teamID, build: build}

		c.cache.Set(envDB.TemplateID, templateInfo, templateInfoExpiration)
	} else {
		templateInfo = item.Value()

		if templateInfo.teamID != teamID && !templateInfo.template.Public {
			return nil, nil, &api.APIError{Code: http.StatusForbidden, ClientMsg: fmt.Sprintf("Team  '%s' does not have access to the template '%s'", teamID, aliasOrEnvID), Err: fmt.Errorf("team  '%s' does not have access to the template '%s'", teamID, aliasOrEnvID)}
		}
	}

	return templateInfo.template, templateInfo.build, nil
}

// Invalidate invalidates the cache for the given templateID
func (c *TemplateCache) Invalidate(templateID string) {
	c.cache.Delete(templateID)
}

func (c *TemplateCache) InvalidateMultiple(templateIDs []string) {
	for _, templateID := range templateIDs {
		c.Invalidate(templateID)
	}
}
