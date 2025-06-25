package template_manager

import (
	"context"
	"time"

	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/emptypb"

	orchestratorinfo "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator-info"
)

var healthCheckInterval = 5 * time.Second

func (tm *TemplateManager) localBuilderHealthCheckSync(ctx context.Context) {
	ticker := time.NewTicker(healthCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			reqCtx, reqCtxCancel := context.WithTimeout(ctx, 5*time.Second)
			res, err := tm.localClient.Info.ServiceInfo(reqCtx, &emptypb.Empty{})
			reqCtxCancel()

			if err != nil {
				zap.L().Error("Failed to get health status of template manager", zap.Error(err))
				tm.localClientMutex.Lock()
				tm.localClientStatus = orchestratorinfo.ServiceInfoStatus_OrchestratorDraining
				tm.localClientMutex.Unlock()
			}

			tm.localClientMutex.Lock()
			tm.localClientStatus = res.ServiceStatus
			zap.L().Debug("Template manager health status", zap.String("status", tm.localClientStatus.String()))
			tm.localClientMutex.Unlock()
		}
	}
}
