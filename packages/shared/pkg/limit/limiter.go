package limit

import (
	"context"
	"sync"

	featureflags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

type Limiter struct {
	gCloudUploadLimiter *utils.AdjustableSemaphore
	featureFlags        *featureflags.Client

	done      chan struct{}
	closeOnce sync.Once
}

func New(ctx context.Context, featureFlags *featureflags.Client) (*Limiter, error) {
	uploadLimiter, err := utils.NewAdjustableSemaphore(int64(featureFlags.IntFlagDefault(featureflags.GcloudConcurrentUploadLimit)))
	if err != nil {
		return nil, err
	}

	l := &Limiter{
		gCloudUploadLimiter: uploadLimiter,
		featureFlags:        featureFlags,
		done:                make(chan struct{}),
	}

	go l.UpdateUploadLimitSemaphore(ctx)

	return l, nil
}

func (l *Limiter) Close(ctx context.Context) error {
	l.closeOnce.Do(func() {
		close(l.done)
	})

	return nil
}
