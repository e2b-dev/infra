package limit

import (
	"context"
	"fmt"

	"github.com/launchdarkly/go-sdk-common/v3/ldcontext"
	"go.uber.org/zap"

	featureflags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

type Limiter struct {
	ctx                 context.Context
	gCloudUploadLimiter *utils.AdjustableSemaphore
	featureFlags        *featureflags.Client

	done chan struct{}
}

func New(featureFlags *featureflags.Client) (*Limiter, error) {
	uploadLimiter, err := utils.NewAdjustableSemaphore(featureflags.GcloudConcurrentUploadLimitDefault)
	if err != nil {
		return nil, err
	}

	l := &Limiter{
		gCloudUploadLimiter: uploadLimiter,
		featureFlags:        featureFlags,
		done:                make(chan struct{}),
	}

	go l.UpdateUploadLimitSemaphore()

	return l, nil
}

func (l *Limiter) Close(ctx context.Context) error {
	if l.done != nil {
		close(l.done)
	}

	return nil
}
