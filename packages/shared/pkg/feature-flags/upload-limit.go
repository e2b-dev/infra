package feature_flags

import (
	"context"
	"time"

	"github.com/launchdarkly/go-sdk-common/v3/ldcontext"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

func (c *Client) UpdateUploadLimitSemaphore(ctx context.Context, uploadSemaphore *utils.AdjustableSemaphore) {
	flagCtx := ldcontext.NewBuilder(GcloudConcurrentUploadLimit).Build()

	ticker := time.NewTicker(5 * time.Second)
	for {
		select {
		case <-ticker.C:
			uploadLimitFlag, flagErr := c.Ld.IntVariation(GcloudConcurrentUploadLimit, flagCtx, GcloudConcurrentUploadLimitDefault)
			if flagErr != nil {
				zap.L().Error("soft failing during metrics write feature flag receive", zap.Error(flagErr))
			}

			// Update the semaphore with the new value
			if err := uploadSemaphore.SetLimit(int64(uploadLimitFlag)); err != nil {
				zap.L().Error("failed to adjust upload semaphore", zap.Error(err))
			}
		case <-ctx.Done():
			ticker.Stop()
			return
		}
	}
}
