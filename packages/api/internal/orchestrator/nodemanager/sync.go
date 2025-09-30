package nodemanager

import (
	"context"

	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox/store/memory"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

const syncMaxRetries = 4

func (n *Node) Sync(ctx context.Context, instanceCache *memory.Store) {
	syncRetrySuccess := false

	for range syncMaxRetries {
		client, ctx := n.GetClient(ctx)
		nodeInfo, err := client.Info.ServiceInfo(ctx, &emptypb.Empty{})
		if err != nil {
			zap.L().Error("Error getting node info", zap.Error(err), logger.WithNodeID(n.ID))
			continue
		}

		// update node status (if changed)
		nodeStatus, ok := OrchestratorToApiNodeStateMapper[nodeInfo.ServiceStatus]
		if !ok {
			zap.L().Error("Unknown service info status", zap.String("status", nodeInfo.ServiceStatus.String()), logger.WithNodeID(n.ID))
			nodeStatus = api.NodeStatusUnhealthy
		}

		n.setStatus(nodeStatus)
		n.setMetadata(
			NodeMetadata{
				ServiceInstanceID: nodeInfo.ServiceId,
				Commit:            nodeInfo.ServiceCommit,
				Version:           nodeInfo.ServiceVersion,
			},
		)
		// Update host metrics from service info
		n.UpdateMetricsFromServiceInfoResponse(nodeInfo)

		activeInstances, instancesErr := n.GetSandboxes(ctx)
		if instancesErr != nil {
			zap.L().Error("Error getting instances", zap.Error(instancesErr), logger.WithNodeID(n.ID))
			continue
		}

		instanceCache.Sync(ctx, activeInstances, n.ID)

		syncRetrySuccess = true
		break
	}

	if !syncRetrySuccess {
		zap.L().Error("Failed to sync node after max retries, temporarily marking as unhealthy", logger.WithNodeID(n.ID))
		n.setStatus(api.NodeStatusUnhealthy)
		return
	}

	builds, buildsErr := n.listCachedBuilds(ctx)
	if buildsErr != nil {
		zap.L().Error("Error listing cached builds", zap.Error(buildsErr), logger.WithNodeID(n.ID))
		return
	}

	n.SyncBuilds(builds)
}
