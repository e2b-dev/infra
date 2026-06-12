package nodemanager

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc/connectivity"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	orchestratorinfo "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator-info"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

var ApiNodeToOrchestratorStateMapper = map[api.NodeStatus]orchestratorinfo.ServiceInfoStatus{
	api.NodeStatusReady:     orchestratorinfo.ServiceInfoStatus_Healthy,
	api.NodeStatusDraining:  orchestratorinfo.ServiceInfoStatus_Draining,
	api.NodeStatusUnhealthy: orchestratorinfo.ServiceInfoStatus_Unhealthy,
	api.NodeStatusStandby:   orchestratorinfo.ServiceInfoStatus_Standby,
}

// StatusInfo bundles the node status with the time of its last change.
type StatusInfo struct {
	Status    api.NodeStatus
	ChangedAt time.Time
}

func (n *Node) Status() api.NodeStatus {
	return n.StatusInfo().Status
}

// StatusInfo returns the node status together with the time of the last status change.
// The returned status can be derived from the gRPC connection state, in which case the
// timestamp still refers to the last stored status change.
func (n *Node) StatusInfo() StatusInfo {
	n.mutex.RLock()
	defer n.mutex.RUnlock()

	status := n.status.Status
	if status == api.NodeStatusReady {
		switch n.client.Connection.GetState() {
		case connectivity.Shutdown:
			status = api.NodeStatusUnhealthy
		case connectivity.TransientFailure, connectivity.Connecting:
			status = api.NodeStatusConnecting
		}
	}

	return StatusInfo{Status: status, ChangedAt: n.status.ChangedAt}
}

// setStatus updates the node status together with the time of the last status change.
// The changedAt value is the timestamp reported by the orchestrator, zero when not available.
func (n *Node) setStatus(ctx context.Context, status api.NodeStatus, changedAt time.Time) {
	n.mutex.Lock()
	defer n.mutex.Unlock()

	if n.status.Status != status {
		logger.L().Info(ctx, "NodeID status changed", logger.WithNodeID(n.ID), zap.String("status", string(status)))
		n.status = StatusInfo{Status: status, ChangedAt: changedAt}
	} else if changedAt.After(n.status.ChangedAt) {
		// Status is the same, but the orchestrator reported a newer change.
		n.status.ChangedAt = changedAt
	}
}

func (n *Node) SendStatusChange(ctx context.Context, s api.NodeStatus) error {
	nodeStatus, ok := ApiNodeToOrchestratorStateMapper[s]
	if !ok {
		logger.L().Error(ctx, "Unknown service info status", zap.String("status", string(s)), logger.WithNodeID(n.ID))

		return fmt.Errorf("unknown service info status: %s", s)
	}

	client, ctx := n.GetClient(ctx)
	_, err := client.Info.ServiceStatusOverride(ctx, &orchestratorinfo.ServiceStatusChangeRequest{ServiceStatus: nodeStatus})
	if err != nil {
		logger.L().Error(ctx, "Failed to send status change", zap.Error(err))

		return err
	}

	return nil
}
