package cache

import (
	"context"
	"fmt"
	"time"

	"github.com/jellydator/ttlcache/v3"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/template/build/buildlogger"
	template_manager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

const (
        buildInfoExpiration = time.Minute * 70 // Must exceed the API-side buildTimeout (1 hour)
)

type BuildInfoResult struct {
	Status   template_manager.TemplateBuildState
	Reason   *template_manager.TemplateBuildStatusReason
	Metadata *template_manager.TemplateBuildMetadata
}

type BuildInfo struct {
	TeamID string
	logs   *buildlogger.LogEntryLogger
	Result *utils.SetOnce[BuildInfoResult]
}

func (b *BuildInfo) IsRunning() bool {
	return b.GetStatus() == template_manager.TemplateBuildState_Building
}

func (b *BuildInfo) GetStatus() template_manager.TemplateBuildState {
	res := b.GetResult()
	if res != nil {
		return res.Status
	}

	// When the build is still in progress, no result is set, so we return the building state.
	return template_manager.TemplateBuildState_Building
}

func (b *BuildInfo) GetResult() *BuildInfoResult {
	res, err := b.Result.Result()
	if err != nil {
		// If the result is not set, it means the build is still in progress.
		return nil
	}

	return &res
}

func (b *BuildInfo) SetSuccess(metadata *template_manager.TemplateBuildMetadata) {
	logger.L().Info(context.Background(), "build marked as SUCCESS in cache",
		zap.String("team.id", b.TeamID),
	)
	_ = b.Result.SetValue(BuildInfoResult{
		Status:   template_manager.TemplateBuildState_Completed,
		Metadata: metadata,
		Reason:   nil,
	})
}

func (b *BuildInfo) SetFail(reason *template_manager.TemplateBuildStatusReason) {
	var msg string
	if reason != nil {
		msg = reason.GetMessage()
	}
	logger.L().Warn(context.Background(), "build marked as FAILED in cache",
		zap.String("team.id", b.TeamID),
		zap.String("reason", msg),
	)
	_ = b.Result.SetValue(BuildInfoResult{
		Status:   template_manager.TemplateBuildState_Failed,
		Reason:   reason,
		Metadata: nil,
	})
}

func (b *BuildInfo) GetLogs() []*template_manager.TemplateBuildLogEntry {
	return b.logs.Lines()
}

type BuildCache struct {
	cache *ttlcache.Cache[string, *BuildInfo]
}

func NewBuildCache(ctx context.Context, meterProvider metric.MeterProvider) *BuildCache {
	meter := meterProvider.Meter("github.com/e2b-dev/infra/packages/orchestrator/pkg/template/cache")

	logger.L().Info(ctx, "creating build cache", zap.Duration("ttl", buildInfoExpiration))

	cache := ttlcache.New(ttlcache.WithTTL[string, *BuildInfo](buildInfoExpiration))

	// Log when cache entries are evicted so we can diagnose premature expiry.
	cache.OnEviction(func(_ context.Context, reason ttlcache.EvictionReason, item *ttlcache.Item[string, *BuildInfo]) {
		buildID := item.Key()
		info := item.Value()
		var status string
		if info != nil {
			status = info.GetStatus().String()
		} else {
			status = "nil"
		}
		logger.L().Warn(ctx, "build cache entry evicted",
			zap.String("build.id", buildID),
			zap.Int("eviction_reason", int(reason)),
			zap.String("build_status", status),
			zap.Duration("ttl", buildInfoExpiration),
		)
	})
	_, err := telemetry.GetObservableUpDownCounter(meter, telemetry.BuildCounterMeterName, func(_ context.Context, observer metric.Int64Observer) error {
		items := utils.MapValues(cache.Items())

		// Group by teamID
		teamCounts := make(map[string]int)
		for _, item := range items {
			if item == nil {
				continue
			}
			build := item.Value()
			if build == nil {
				continue
			}

			teamID := build.TeamID

			// Include 0 too to reset the counter if the team has no builds anymore
			_, ok := teamCounts[teamID]
			if !ok {
				teamCounts[teamID] = 0
			}

			// Count running builds
			if build.IsRunning() {
				teamCounts[teamID]++
			}
		}

		for teamID, count := range teamCounts {
			observer.Observe(int64(count), metric.WithAttributes(telemetry.WithTeamID(teamID)))
		}

		return nil
	})
	if err != nil {
		logger.L().Error(ctx, "error creating counter", zap.Error(err), zap.String("counter_name", string(telemetry.BuildCounterMeterName)))
	}

	go cache.Start()

	return &BuildCache{
		cache: cache,
	}
}

// Get returns the build info.
func (c *BuildCache) Get(buildID string) (*BuildInfo, error) {
	item := c.cache.Get(buildID)
	if item == nil {
		// Log all current cache keys to help diagnose missing entries.
		var keys []string
		for k := range c.cache.Items() {
			keys = append(keys, k)
		}
		logger.L().Warn(context.Background(), "build cache miss",
			zap.String("build.id", buildID),
			zap.Int("cache_size", len(keys)),
			zap.Strings("cached_builds", keys),
		)
		return nil, fmt.Errorf("build %s not found in cache (cache has %d entries)", buildID, len(keys))
	}

	value := item.Value()
	if value == nil {
		logger.L().Warn(context.Background(), "build cache hit but nil value",
			zap.String("build.id", buildID),
		)
		return nil, fmt.Errorf("build %s not found in cache (nil value)", buildID)
	}

	return value, nil
}

// Create creates a new build if it doesn't exist in the cache or the build was already finished.
func (c *BuildCache) Create(teamID string, buildID string, logs *buildlogger.LogEntryLogger) (*BuildInfo, error) {
	info := &BuildInfo{
		TeamID: teamID,
		logs:   logs,
		Result: utils.NewSetOnce[BuildInfoResult](),
	}

	_, found := c.cache.GetOrSet(buildID, info,
		ttlcache.WithTTL[string, *BuildInfo](buildInfoExpiration),
	)
	if found {
		return nil, fmt.Errorf("build %s already exists in cache", buildID)
	}

	logger.L().Info(context.Background(), "build cache entry created",
		zap.String("build.id", buildID),
		zap.String("team.id", teamID),
		zap.Duration("ttl", buildInfoExpiration),
		zap.Int("cache_size", c.cache.Len()),
	)

	return info, nil
}

func (c *BuildCache) Delete(buildID string) {
	logger.L().Info(context.Background(), "build cache entry explicitly deleted",
		zap.String("build.id", buildID),
	)
	c.cache.Delete(buildID)
}
