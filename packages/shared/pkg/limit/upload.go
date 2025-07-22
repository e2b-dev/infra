package limit

import (
	"time"

	"github.com/launchdarkly/go-sdk-common/v3/ldcontext"
	"go.uber.org/zap"

	featureflags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
)

func (l *Limiter) UpdateUploadLimitSemaphore() {
	flagCtx := ldcontext.NewBuilder(featureflags.GcloudConcurrentUploadLimit).Build()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			uploadLimitFlag, flagErr := l.featureFlags.Ld.IntVariation(featureflags.GcloudConcurrentUploadLimit, flagCtx, featureflags.GcloudConcurrentUploadLimitDefault)
			if flagErr != nil {
				zap.L().Warn("soft failing during metrics write feature flag receive", zap.Error(flagErr))
			}

			// Update the semaphore with the new value
			if err := l.gCloudUploadLimiter.SetLimit(int64(uploadLimitFlag)); err != nil {
				zap.L().Error("failed to adjust upload semaphore", zap.Error(err))
			}
		case <-l.done:
			return
		}
	}
}
