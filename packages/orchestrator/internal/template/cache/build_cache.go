package cache

import (
	"context"
	"fmt"
	"time"

	"github.com/jellydator/ttlcache/v3"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"

	template_manager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

const (
	buildInfoExpiration = time.Minute * 10 // 10 minutes
)

var CancelledBuildReason = "build was cancelled"

type BuildInfoResult struct {
	Status   template_manager.TemplateBuildState
	Reason   *string
	Metadata *template_manager.TemplateBuildMetadata
}

type BuildInfo struct {
	logs   *SafeBuffer
	Result *utils.SetOnce[BuildInfoResult]
}

func (b *BuildInfo) IsRunning() bool {
	return b.GetStatus() == template_manager.TemplateBuildState_Building
}

func (b *BuildInfo) IsFailed() bool {
	return b.GetStatus() == template_manager.TemplateBuildState_Failed
}

func (b *BuildInfo) GetMetadata() *template_manager.TemplateBuildMetadata {
	res, err := b.Result.Result()
	if err != nil {
		return nil
	}

	return res.Metadata
}

func (b *BuildInfo) GetStatus() template_manager.TemplateBuildState {
	res, err := b.Result.Result()
	if err != nil {
		return template_manager.TemplateBuildState_Building
	}

	return res.Status
}

func (b *BuildInfo) GetReason() *string {
	res, err := b.Result.Result()
	if err != nil {
		return nil
	}

	return res.Reason
}

func (b *BuildInfo) SetSuccess(metadata *template_manager.TemplateBuildMetadata) {
	_ = b.Result.SetValue(BuildInfoResult{
		Status:   template_manager.TemplateBuildState_Completed,
		Metadata: metadata,
		Reason:   nil,
	})
}

func (b *BuildInfo) SetFail(reason *string) {
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
	_, err := telemetry.GetObservableUpDownCounter(meter, telemetry.BuildCounterMeterName, func(ctx context.Context, observer metric.Int64Observer) error {
		items := utils.MapValues(cache.Items())

		// Filter running builds
		runningCount := len(utils.Filter(items, func(item *ttlcache.Item[string, *BuildInfo]) bool {
			return item != nil && item.Value() != nil && item.Value().IsRunning()
		}))

		observer.Observe(int64(runningCount))
		return nil
	})
	if err != nil {
		zap.L().Error("error creating counter", zap.Error(err), zap.Any("counter_name", telemetry.BuildCounterMeterName))
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
func (c *BuildCache) Create(buildID string, logs *SafeBuffer) (*BuildInfo, error) {
	info := &BuildInfo{
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
