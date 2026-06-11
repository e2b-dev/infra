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

func (n *Node) Status() api.NodeStatus {
	n.mutex.RLock()
	defer n.mutex.RUnlock()

	if n.status != api.NodeStatusReady {
		return n.status
	}

	switch n.client.Connection.GetState() {
	case connectivity.Shutdown:
		return api.NodeStatusUnhealthy
	case connectivity.TransientFailure:
		return api.NodeStatusConnecting
	case connectivity.Connecting:
		return api.NodeStatusConnecting
	default:
		return n.status
	}
}

// StatusChangedAt returns the time of the last node status change.
func (n *Node) StatusChangedAt() time.Time {
	n.mutex.RLock()
	defer n.mutex.RUnlock()

	return n.statusChangedAt
}

// setStatus updates the node status together with the time of the last status change.
// The changedAt value is the timestamp reported by the orchestrator, zero when not available.
func (n *Node) setStatus(ctx context.Context, status api.NodeStatus, changedAt time.Time) {
	n.mutex.Lock()
	defer n.mutex.Unlock()

	if n.status != status {
		logger.L().Info(ctx, "NodeID status changed", logger.WithNodeID(n.ID), zap.String("status", string(status)))
		n.status = status

		if changedAt.IsZero() {
			changedAt = time.Now()
		}
		n.statusChangedAt = changedAt
	} else if changedAt.After(n.statusChangedAt) {
		// Status is the same from the API perspective, but the orchestrator reported a newer change.
		n.statusChangedAt = changedAt
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
