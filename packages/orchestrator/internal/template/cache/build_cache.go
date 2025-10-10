package cache

import (
	"context"
	"fmt"
	"time"

	"github.com/jellydator/ttlcache/v3"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/buildlogger"
	template_manager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

const (
	buildInfoExpiration = time.Minute * 10 // 10 minutes
)

const CanceledBuildReason = "build was cancelled"

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
	_ = b.Result.SetValue(BuildInfoResult{
		Status:   template_manager.TemplateBuildState_Completed,
		Metadata: metadata,
		Reason:   nil,
	})
}

func (b *BuildInfo) SetFail(reason *template_manager.TemplateBuildStatusReason) {
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

func NewBuildCache(meterProvider metric.MeterProvider) *BuildCache {
	meter := meterProvider.Meter("orchestrator.cache.build")

	cache := ttlcache.New(ttlcache.WithTTL[string, *BuildInfo](buildInfoExpiration))
	_, err := telemetry.GetObservableUpDownCounter(meter, telemetry.BuildCounterMeterName, func(_ context.Context, observer metric.Int64Observer) error {
		items := utils.MapValues(cache.Items())

		// Group by teamID
		teamCounts := make(map[string]int)
		for _, item := range items {
			// Filter only running builds
			if item != nil && item.Value() != nil && item.Value().IsRunning() {
				teamID := item.Value().TeamID
				teamCounts[teamID]++
			}
		}

		for teamID, count := range teamCounts {
			observer.Observe(int64(count), metric.WithAttributes(telemetry.WithTeamID(teamID)))
		}
		return nil
	})
	if err != nil {
		zap.L().Error("error creating counter", zap.Error(err), zap.String("counter_name", string(telemetry.BuildCounterMeterName)))
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
		return nil, fmt.Errorf("build %s not found in cache", buildID)
	}

	value := item.Value()
	if value == nil {
		return nil, fmt.Errorf("build %s not found in cache", buildID)
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
		ttlcache.WithDisableTouchOnHit[string, *BuildInfo](),
	)
	if found {
		return nil, fmt.Errorf("build %s already exists in cache", buildID)
	}

	return info, nil
}

func (c *BuildCache) Delete(buildID string) {
	c.cache.Delete(buildID)
}
