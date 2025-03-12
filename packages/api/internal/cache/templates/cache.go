package templatecache

import (
	"context"
	"errors"
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

type TemplateBuildInfo struct {
	TeamID     uuid.UUID
	TemplateID string
}

type TemplateBuildInfoNotFound struct{ error }

func (TemplateBuildInfoNotFound) Error() string {
	return "Template build info not found"
}

type TemplatesBuildCache struct {
	cache *ttlcache.Cache[uuid.UUID, *TemplateBuildInfo]
	db    *db.DB
}

func NewTemplateBuildCache(db *db.DB) *TemplatesBuildCache {
	cache := ttlcache.New(ttlcache.WithTTL[uuid.UUID, *TemplateBuildInfo](templateInfoExpiration))
	go cache.Start()

	return &TemplatesBuildCache{
		cache: cache,
		db:    db,
	}
}

func (c *TemplatesBuildCache) Get(ctx context.Context, buildID uuid.UUID, templateID string) (*TemplateBuildInfo, error) {
	item := c.cache.Get(buildID)
	if item == nil {
		print("build cache: not present in cache")

		envDB, _, envDBErr := c.db.GetEnv(ctx, templateID)
		if envDBErr != nil {
			if errors.Is(envDBErr, db.TemplateNotFound{}) {
				return nil, TemplateBuildInfoNotFound{}
			}

			return nil, fmt.Errorf("failed to get template '%s': %w", buildID, envDBErr)
		}

		// making sure associated template build really exists
		_, envBuildDBErr := c.db.GetEnvBuild(ctx, buildID)
		if envBuildDBErr != nil {
			if errors.Is(envBuildDBErr, db.TemplateBuildNotFound{}) {
				return nil, TemplateBuildInfoNotFound{}
			}

			return nil, fmt.Errorf("failed to get template build '%s': %w", buildID, envBuildDBErr)
		}

		item = c.cache.Set(
			buildID,
			&TemplateBuildInfo{
				TeamID:     envDB.TeamID,
				TemplateID: envDB.TemplateID,
			},
			templateInfoExpiration,
		)

		return item.Value(), nil
	}

	print("build cache: hitting")

	return item.Value(), nil
}
