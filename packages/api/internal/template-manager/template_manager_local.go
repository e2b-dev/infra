package template_manager

import (
	"context"
	"time"

	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/e2b-dev/infra/packages/api/internal/utils"
	orchestratorinfo "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator-info"
)

var (
	healthCheckInterval = 5 * time.Second
	healthCheckTimeout  = 5 * time.Second
)

func (tm *TemplateManager) localClientPeriodicHealthSync(ctx context.Context) {
	// Initial health check to set the initial status
	tm.localClientHealthSync(ctx)

	ticker := time.NewTicker(healthCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			tm.localClientHealthSync(ctx)
		}
	}
}

func (tm *TemplateManager) localClientHealthSync(ctx context.Context) {
	reqCtx, reqCtxCancel := context.WithTimeout(ctx, healthCheckTimeout)
	res, err := tm.localClient.Info.ServiceInfo(reqCtx, &emptypb.Empty{})
	reqCtxCancel()

	err = utils.UnwrapGRPCError(err)
	if err != nil {
		zap.L().Error("Failed to get health status of template manager", zap.Error(err))
		tm.setLocalClientStatus(orchestratorinfo.ServiceInfoStatus_Unhealthy)
		return
	}

	tm.setLocalClientStatus(res.ServiceStatus)
}

func (tm *TemplateManager) setLocalClientStatus(s orchestratorinfo.ServiceInfoStatus) {
	tm.localClientMutex.RLock()
	defer tm.localClientMutex.RUnlock()
	tm.localClientStatus = s
}

func (tm *TemplateManager) GetLocalClientStatus() orchestratorinfo.ServiceInfoStatus {
	tm.localClientMutex.Lock()
	defer tm.localClientMutex.Unlock()
	return tm.localClientStatus
}
