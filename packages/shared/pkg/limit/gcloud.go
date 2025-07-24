package limit

import (
	"fmt"

	"go.uber.org/zap"

	featureflags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

func (l *Limiter) GCloudUploadLimiter() *utils.AdjustableSemaphore {
	return l.gCloudUploadLimiter
}

func (l *Limiter) GCloudCmdLimits(path string) []string {
	maxCPU, flagErr := l.featureFlags.IntFlag(featureflags.GcloudMaxCPUQuota, path)
	if flagErr != nil {
		zap.L().Warn("soft failing during gcloud cmd limits feature flag receive", zap.Error(flagErr), zap.Int("maxCPU", maxCPU))
	}

	maxMemory, flagErr := l.featureFlags.IntFlag(featureflags.GcloudMaxMemoryLimitMiB, path)
	if flagErr != nil {
		zap.L().Warn("soft failing during gcloud cmd limits feature flag receive", zap.Error(flagErr), zap.Int("maxMemory", maxMemory))
	}

	maxTasks, flagErr := l.featureFlags.IntFlag(featureflags.GcloudMaxTasks, path)
	if flagErr != nil {
		zap.L().Warn("soft failing during gcloud cmd limits feature flag receive", zap.Error(flagErr), zap.Int("maxTasks", maxTasks))
	}

	return []string{
		fmt.Sprintf("--property=CPUQuota=%d%%", maxCPU),
		fmt.Sprintf("--property=MemoryMax=%dM", maxMemory),
		// Not 100% sure how this can internally affect the gcloud (probably returning retryable errors from fork there).
		fmt.Sprintf("--property=TasksMax=%d", maxTasks),
	}
}
