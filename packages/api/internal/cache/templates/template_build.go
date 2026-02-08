package templatecache

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"

	"github.com/google/uuid"
	"github.com/jellydator/ttlcache/v3"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/utils"
	sqlcdb "github.com/e2b-dev/infra/packages/db/client"
	"github.com/e2b-dev/infra/packages/db/pkg/types"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	sharedUtils "github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

type TemplateBuildInfo struct {
	TeamID      uuid.UUID
	TemplateID  string
	BuildStatus types.BuildStatus
	Reason      types.BuildReason
	Version     *string

	ClusterID uuid.UUID
	NodeID    *string
}

type TemplateBuildInfoNotFoundError struct{}

func (TemplateBuildInfoNotFoundError) Error() string {
	return "Template build info not found"
}

type TemplatesBuildCache struct {
	cache *ttlcache.Cache[uuid.UUID, TemplateBuildInfo]
	db    *sqlcdb.Client
	mx    sync.Mutex
}

func NewTemplateBuildCache(db *sqlcdb.Client) *TemplatesBuildCache {
	cache := ttlcache.New(ttlcache.WithTTL[uuid.UUID, TemplateBuildInfo](templateInfoExpiration))
	go cache.Start()

	return &TemplatesBuildCache{
		cache: cache,
		db:    db,
	}
}

func (c *TemplatesBuildCache) SetStatus(ctx context.Context, buildID uuid.UUID, status types.BuildStatus, reason types.BuildReason) {
	c.mx.Lock()
	defer c.mx.Unlock()

	cacheItem := c.cache.Get(buildID)
	if cacheItem == nil {
		return
	}

	item := cacheItem.Value()

	logger.L().Info(ctx, "Setting template build status",
		logger.WithBuildID(buildID.String()),
		zap.String("to_status", string(status)),
		zap.String("from_status", string(item.BuildStatus)),
		zap.String("reason", reason.Message),
		zap.String("step", sharedUtils.Sprintp(reason.Step)),
		zap.String("version", sharedUtils.Sprintp(item.Version)),
	)

	_ = c.cache.Set(
		buildID,
		TemplateBuildInfo{
			TeamID:      item.TeamID,
			TemplateID:  item.TemplateID,
			BuildStatus: status,
			Reason:      reason,
			Version:     item.Version,

			ClusterID: item.ClusterID,
			NodeID:    item.NodeID,
		},
		templateInfoExpiration,
	)
}

func (c *TemplatesBuildCache) Get(ctx context.Context, buildID uuid.UUID, templateID string) (TemplateBuildInfo, error) {
	item := c.cache.Get(buildID)
	if item == nil {
		logger.L().Debug(ctx, "Template build info not found in cache, fetching from DB", logger.WithBuildID(buildID.String()))

		result, err := c.db.GetTemplateBuildWithTemplate(ctx, queries.GetTemplateBuildWithTemplateParams{
			TemplateID: templateID,
			BuildID:    buildID,
		})
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return TemplateBuildInfo{}, TemplateBuildInfoNotFoundError{}
			}

			return TemplateBuildInfo{}, fmt.Errorf("failed to get template build '%s': %w", buildID, err)
		}

		item = c.cache.Set(
			buildID,
			TemplateBuildInfo{
				TeamID:      result.Env.TeamID,
				TemplateID:  result.Env.ID,
				BuildStatus: result.EnvBuild.Status,
				Reason:      result.EnvBuild.Reason,
				Version:     result.EnvBuild.Version,

				ClusterID: utils.WithClusterFallback(result.Env.ClusterID),
				NodeID:    result.EnvBuild.ClusterNodeID,
			},
			templateInfoExpiration,
		)

		return item.Value(), nil
	}

	return item.Value(), nil
}

// GetRunningBuildsForTeam returns all running builds for the given teamID
// This is a simple implementation of concurrency limit
// It does not guarantee that the limit is not exceeded, but it should be good enough for now (considering overall low number of total builds)
func (c *TemplatesBuildCache) GetRunningBuildsForTeam(teamID uuid.UUID) []TemplateBuildInfo {
	var builds []TemplateBuildInfo
	for _, item := range c.cache.Items() {
		value := item.Value()
		isRunning := value.BuildStatus.IsInProgress() || value.BuildStatus.IsPending()
		if value.TeamID == teamID && isRunning {
			builds = append(builds, value)
		}
	}

	return builds
}
