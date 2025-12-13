package limit

import (
	"context"
	"time"

	"go.uber.org/zap"

	featureflags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

func (l *Limiter) UpdateUploadLimitSemaphore(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			uploadLimitFlag := l.featureFlags.IntFlag(ctx, featureflags.GcloudConcurrentUploadLimit)

			// Update the semaphore with the new value
			if err := l.gCloudUploadLimiter.SetLimit(int64(uploadLimitFlag)); err != nil {
				logger.L().Error(ctx, "failed to adjust upload semaphore", zap.Error(err))
			}
		case <-l.done:
			return
		}
	}
}
