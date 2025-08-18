package templatecache

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jellydator/ttlcache/v3"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	sqlcdb "github.com/e2b-dev/infra/packages/db/client"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/db"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/models/envbuild"
	"github.com/e2b-dev/infra/packages/shared/pkg/schema"
)

const templateInfoExpiration = 5 * time.Minute

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

type TemplateCache struct {
	cache      *ttlcache.Cache[string, *TemplateInfo]
	db         *sqlcdb.Client
	aliasCache *AliasCache
}

func NewTemplateCache(db *sqlcdb.Client) *TemplateCache {
	cache := ttlcache.New(ttlcache.WithTTL[string, *TemplateInfo](templateInfoExpiration))
	aliasCache := NewAliasCache()
	go cache.Start()

	return &TemplateCache{
		cache:      cache,
		db:         db,
		aliasCache: aliasCache,
	}
}

func (c *TemplateCache) Get(ctx context.Context, aliasOrEnvID string, teamID uuid.UUID, clusterID uuid.UUID, public bool) (*api.Template, *queries.EnvBuild, *api.APIError) {
	var item *ttlcache.Item[string, *TemplateInfo]
	var templateInfo *TemplateInfo

	var build *queries.EnvBuild

	templateID, found := c.aliasCache.Get(aliasOrEnvID)
	if found == true {
		item = c.cache.Get(templateID)
	}

	if item == nil {
		result, err := c.db.GetEnvWithBuild(ctx, aliasOrEnvID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, nil, &api.APIError{Code: http.StatusNotFound, ClientMsg: fmt.Sprintf("template '%s' not found", aliasOrEnvID), Err: err}
			}

			return nil, nil, &api.APIError{Code: http.StatusInternalServerError, ClientMsg: fmt.Sprintf("error while getting template: %v", err), Err: err}
		}

		build = &result.EnvBuild
		template := result.Env
		aliases := result.Aliases

		cluster := uuid.Nil
		if template.ClusterID != nil {
			cluster = *template.ClusterID
		}

		c.aliasCache.cache.Set(template.ID, template.ID, templateInfoExpiration)
		for _, alias := range aliases {
			c.aliasCache.cache.Set(alias, template.ID, templateInfoExpiration)
		}

		// Check if the team has access to the environment
		if template.TeamID != teamID && (!public || !template.Public) {
			return nil, nil, &api.APIError{Code: http.StatusForbidden, ClientMsg: fmt.Sprintf("Team  '%s' does not have access to the template '%s'", teamID, aliasOrEnvID), Err: fmt.Errorf("team  '%s' does not have access to the template '%s'", teamID, aliasOrEnvID)}
		}

		if cluster != clusterID {
			return nil, nil, &api.APIError{Code: http.StatusBadRequest, ClientMsg: fmt.Sprintf("Template '%s' is not available in requested cluster", aliasOrEnvID), Err: fmt.Errorf("template '%s' is not available in requested cluster '%s'", aliasOrEnvID, clusterID)}
		}

		templateInfo = &TemplateInfo{
			template: &api.Template{
				TemplateID: template.ID,
				BuildID:    build.ID.String(),
				Public:     template.Public,
				Aliases:    &aliases,
			},
			teamID:    teamID,
			clusterID: clusterID,
			build:     build,
		}

		c.cache.Set(template.ID, templateInfo, templateInfoExpiration)
	} else {
		templateInfo = item.Value()
		build = templateInfo.build

		if templateInfo.teamID != teamID && !templateInfo.template.Public {
			return nil, nil, &api.APIError{Code: http.StatusForbidden, ClientMsg: fmt.Sprintf("Team  '%s' does not have access to the template '%s'", teamID, aliasOrEnvID), Err: fmt.Errorf("team  '%s' does not have access to the template '%s'", teamID, aliasOrEnvID)}
		}

		if templateInfo.clusterID != clusterID {
			return nil, nil, &api.APIError{Code: http.StatusBadRequest, ClientMsg: fmt.Sprintf("Template '%s' is not available in requested cluster", aliasOrEnvID), Err: fmt.Errorf("template '%s' is not available in requested cluster '%s'", aliasOrEnvID, clusterID)}
		}
	}

	return templateInfo.template, build, nil
}

// Invalidate invalidates the cache for the given templateID
func (c *TemplateCache) Invalidate(templateID string) {
	c.cache.Delete(templateID)
}

type TemplateBuildInfo struct {
	TeamID      uuid.UUID
	TemplateID  string
	BuildStatus envbuild.Status
	Reason      *schema.BuildReason

	ClusterID     *uuid.UUID
	ClusterNodeID *string
}

type TemplateBuildInfoNotFound struct{ error }

func (TemplateBuildInfoNotFound) Error() string {
	return "Template build info not found"
}

type TemplatesBuildCache struct {
	cache *ttlcache.Cache[uuid.UUID, TemplateBuildInfo]
	db    *db.DB
	mx    sync.Mutex
}

func NewTemplateBuildCache(db *db.DB) *TemplatesBuildCache {
	cache := ttlcache.New(ttlcache.WithTTL[uuid.UUID, TemplateBuildInfo](templateInfoExpiration))
	go cache.Start()

	return &TemplatesBuildCache{
		cache: cache,
		db:    db,
	}
}

func (c *TemplatesBuildCache) SetStatus(buildID uuid.UUID, status envbuild.Status, reason *schema.BuildReason) {
	c.mx.Lock()
	defer c.mx.Unlock()

	cacheItem := c.cache.Get(buildID)
	if cacheItem == nil {
		return
	}

	item := cacheItem.Value()

	zap.L().Info("Setting template build status",
		logger.WithBuildID(buildID.String()),
		zap.String("to_status", status.String()),
		zap.String("from_status", item.BuildStatus.String()),
		zap.Any("reason", reason),
	)

	_ = c.cache.Set(
		buildID,
		TemplateBuildInfo{
			TeamID:      item.TeamID,
			TemplateID:  item.TemplateID,
			BuildStatus: status,
			Reason:      reason,

			ClusterID:     item.ClusterID,
			ClusterNodeID: item.ClusterNodeID,
		},
		templateInfoExpiration,
	)
}

func (c *TemplatesBuildCache) Get(ctx context.Context, buildID uuid.UUID, templateID string) (TemplateBuildInfo, error) {
	item := c.cache.Get(buildID)
	if item == nil {
		zap.L().Debug("Template build info not found in cache, fetching from DB", logger.WithBuildID(buildID.String()))

		envDB, envDBErr := c.db.GetEnv(ctx, templateID)
		if envDBErr != nil {
			if errors.Is(envDBErr, db.TemplateNotFound{}) {
				return TemplateBuildInfo{}, TemplateBuildInfoNotFound{}
			}

			return TemplateBuildInfo{}, fmt.Errorf("failed to get template '%s': %w", buildID, envDBErr)
		}

		// making sure associated template build really exists
		envBuildDB, envBuildDBErr := c.db.GetEnvBuild(ctx, buildID)
		if envBuildDBErr != nil {
			if errors.Is(envBuildDBErr, db.TemplateBuildNotFound{}) {
				return TemplateBuildInfo{}, TemplateBuildInfoNotFound{}
			}

			return TemplateBuildInfo{}, fmt.Errorf("failed to get template build '%s': %w", buildID, envBuildDBErr)
		}

		item = c.cache.Set(
			buildID,
			TemplateBuildInfo{
				TeamID:      envDB.TeamID,
				TemplateID:  envDB.ID,
				BuildStatus: envBuildDB.Status,
				Reason:      envBuildDB.Reason,

				ClusterID:     envDB.ClusterID,
				ClusterNodeID: envBuildDB.ClusterNodeID,
			},
			templateInfoExpiration,
		)

		return item.Value(), nil
	}

	zap.L().Debug("Template build info found in cache", logger.WithBuildID(buildID.String()))

	return item.Value(), nil
}
