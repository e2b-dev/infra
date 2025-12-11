package ioc

import (
	"context"
	"time"

	"go.uber.org/fx"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/service"
	"github.com/e2b-dev/infra/packages/shared/pkg/env"
	orchestratorinfo "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator-info"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

func startServiceInfo(lc fx.Lifecycle, info *service.ServiceInfo, logger logger.Logger) {
	lc.Append(fx.Hook{
		OnStop: func(ctx context.Context) error {
			logger.Info(ctx, "shutting down service info")
			defer logger.Info(ctx, "service info shutdown complete")

			// Mark service draining if not already.
			// If status was previously changed via API, we don't want to override it.
			if info.GetStatus() == orchestratorinfo.ServiceInfoStatus_Healthy {
				info.SetStatus(ctx, orchestratorinfo.ServiceInfoStatus_Draining)

				// Wait for draining state to propagate to all consumers
				if !env.IsLocal() {
					time.Sleep(15 * time.Second)
				}
			}

			return nil
		},
	})
}
