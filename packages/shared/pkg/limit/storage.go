package limit

import (
	"context"

	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

func (l *Limiter) StorageUploadLimiter() *utils.AdjustableSemaphore {
	return l.storageUploadLimiter
}

func (l *Limiter) StorageMaxUploadTasks(ctx context.Context) int {
	maxTasks := l.featureFlags.IntFlag(ctx, featureflags.StorageMaxUploadTasks)

	return maxTasks
}

func StorageMaxUploadTasksDefault() int {
	return featureflags.StorageMaxUploadTasks.Fallback()
}
