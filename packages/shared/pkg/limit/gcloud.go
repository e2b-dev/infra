package limit

import (
	"go.uber.org/zap"

	featureflags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

func (l *Limiter) GCloudUploadLimiter() *utils.AdjustableSemaphore {
	return l.gCloudUploadLimiter
}

func (l *Limiter) GCloudMaxTasks() int {
	maxTasks, flagErr := l.featureFlags.IntFlag(featureflags.GcloudMaxTasks, "gcloud")
	if flagErr != nil {
		zap.L().Warn("soft failing during gcloud max tasks feature flag receive", zap.Error(flagErr), zap.Int("maxTasks", maxTasks))
	}

	return maxTasks
}
