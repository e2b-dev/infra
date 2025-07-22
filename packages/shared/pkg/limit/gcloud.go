package limit

import (
	"fmt"

	"github.com/launchdarkly/go-sdk-common/v3/ldcontext"
	"go.uber.org/zap"

	featureflags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

func (l *Limiter) GCloudUploadLimiter() *utils.AdjustableSemaphore {
	return l.gCloudUploadLimiter
}

func (l *Limiter) GCloudCmdLimits(path string) []string {
	flagCtx := ldcontext.NewBuilder(featureflags.GcloudMaxCPUQuota).SetString("path", path).Build()
	maxCPU, flagErr := l.featureFlags.Ld.IntVariation(featureflags.GcloudMaxCPUQuota, flagCtx, featureflags.GcloudMaxCPUQuotaDefault)
	if flagErr != nil {
		zap.L().Warn("soft failing during metrics write feature flag receive", zap.Error(flagErr))
	}

	flagCtx = ldcontext.NewBuilder(featureflags.GcloudMaxMemoryLimitMiB).SetString("path", path).Build()
	maxMemory, flagErr := l.featureFlags.Ld.IntVariation(featureflags.GcloudMaxMemoryLimitMiB, flagCtx, featureflags.GcloudMaxMemoryLimitMiBDefault)
	if flagErr != nil {
		zap.L().Warn("soft failing during metrics write feature flag receive", zap.Error(flagErr))
	}

	flagCtx = ldcontext.NewBuilder(featureflags.GcloudMaxTasks).SetString("path", path).Build()
	maxTasks, flagErr := l.featureFlags.Ld.IntVariation(featureflags.GcloudMaxTasks, flagCtx, featureflags.GcloudMaxTasksDefault)
	if flagErr != nil {
		zap.L().Warn("soft failing during metrics write feature flag receive", zap.Error(flagErr))
	}

	return []string{
		fmt.Sprintf("--property=CPUQuota=%d%%", maxCPU),
		fmt.Sprintf("--property=MemoryMax=%dM", maxMemory),
		// Not 100% sure how this can internally affect the gcloud (probably returning retryable errors from fork there).
		fmt.Sprintf("--property=TasksMax=%d", maxTasks),
	}
}
