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

// ApiNodeToOrchestratorStateMapper translates an api.NodeStatus into the status
// an orchestrator understands. It has callers with different needs. Adding an entry
// purely to make a status overridable therefore also makes it routable if its
// orchestrator counterpart is. TestStatusMappersAreInverses pins this map
// against OrchestratorToApiNodeStateMapper so the two cannot drift apart.
var ApiNodeToOrchestratorStateMapper = map[api.NodeStatus]orchestratorinfo.ServiceInfoStatus{
	api.NodeStatusReady:     orchestratorinfo.ServiceInfoStatus_Healthy,
	api.NodeStatusDraining:  orchestratorinfo.ServiceInfoStatus_Draining,
	api.NodeStatusUnhealthy: orchestratorinfo.ServiceInfoStatus_Unhealthy,
	api.NodeStatusStandby:   orchestratorinfo.ServiceInfoStatus_Standby,
}

type StatusInfo struct {
	Status    api.NodeStatus
	ChangedAt time.Time
}

func (n *Node) Status() api.NodeStatus {
	return n.StatusInfo().Status
}

// CanAcceptNewRequests reports whether the node may be sent new requests. A
// status with no orchestrator counterpart — api.NodeStatusConnecting, which is
// derived from the local gRPC channel state — is treated as unroutable.
func (n *Node) CanAcceptNewRequests() bool {
	status, ok := ApiNodeToOrchestratorStateMapper[n.Status()]

	return ok && status.CanAcceptNewRequests()
}

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

func (n *Node) setStatus(ctx context.Context, status api.NodeStatus, changedAt time.Time) {
	n.mutex.Lock()
	defer n.mutex.Unlock()

	if n.status.Status != status {
		logger.L().Info(ctx, "NodeID status changed", logger.WithNodeID(n.ID), zap.String("status", string(status)))
	}

	n.status = StatusInfo{Status: status, ChangedAt: changedAt}
}

// markUnhealthyLocal marks the node as unhealthy based on a local observation.
// If the node is already unhealthy, the original transition time is preserved
// so the status change timestamp reflects when the node first became unhealthy.
func (n *Node) markUnhealthyLocal(ctx context.Context) {
	n.mutex.Lock()
	defer n.mutex.Unlock()

	if n.status.Status == api.NodeStatusUnhealthy {
		return
	}

	logger.L().Info(ctx, "NodeID status changed", logger.WithNodeID(n.ID), zap.String("status", string(api.NodeStatusUnhealthy)))
	n.status = StatusInfo{Status: api.NodeStatusUnhealthy, ChangedAt: time.Now()}
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
