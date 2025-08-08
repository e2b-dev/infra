package template_manager

import (
	"context"
	"time"

	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/e2b-dev/infra/packages/api/internal/utils"
	orchestratorinfo "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator-info"
)

const (
	unknownNodeID = "unknown"
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
		tm.setLocalClientInfo(orchestratorinfo.ServiceInfoStatus_Unhealthy, unknownNodeID)
		return
	}

	tm.setLocalClientInfo(res.ServiceStatus, res.NodeId)
}

func (tm *TemplateManager) setLocalClientInfo(status orchestratorinfo.ServiceInfoStatus, nodeID string) {
	tm.localClientMutex.Lock()
	defer tm.localClientMutex.Unlock()

	tm.localClientInfo = LocalTemplateManagerInfo{
		status: status,
		nodeID: nodeID,
	}
}

func (tm *TemplateManager) GetLocalClientInfo() LocalTemplateManagerInfo {
	tm.localClientMutex.Lock()
	defer tm.localClientMutex.Unlock()
	return tm.localClientInfo
}
