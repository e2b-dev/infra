package limit

import (
	"context"
	"time"

	"go.uber.org/zap"

	featureflags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
)

func (l *Limiter) UpdateUploadLimitSemaphore(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			uploadLimitFlag, flagErr := l.featureFlags.IntFlag(ctx, featureflags.GcloudConcurrentUploadLimit)
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
