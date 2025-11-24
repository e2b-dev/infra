package nodemanager

import (
	"context"

	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

const syncMaxRetries = 4

func (n *Node) Sync(ctx context.Context, store *sandbox.Store) {
	syncRetrySuccess := false

	for range syncMaxRetries {
		client, ctx := n.GetClient(ctx)
		nodeInfo, err := client.Info.ServiceInfo(ctx, &emptypb.Empty{})
		if err != nil {
			logger.L().Error(ctx, "Error getting node info", zap.Error(err), logger.WithNodeID(n.ID))

			continue
		}

		// update node status (if changed)
		nodeStatus, ok := OrchestratorToApiNodeStateMapper[nodeInfo.GetServiceStatus()]
		if !ok {
			logger.L().Error(ctx, "Unknown service info status", zap.String("status", nodeInfo.GetServiceStatus().String()), logger.WithNodeID(n.ID))
			nodeStatus = api.NodeStatusUnhealthy
		}

		n.setStatus(ctx, nodeStatus)
		n.setMetadata(
			NodeMetadata{
				ServiceInstanceID: nodeInfo.GetServiceId(),
				Commit:            nodeInfo.GetServiceCommit(),
				Version:           nodeInfo.GetServiceVersion(),
			},
		)
		// Update host metrics from service info
		n.UpdateMetricsFromServiceInfoResponse(nodeInfo)

		activeInstances, instancesErr := n.GetSandboxes(ctx)
		if instancesErr != nil {
			logger.L().Error(ctx, "Error getting instances", zap.Error(instancesErr), logger.WithNodeID(n.ID))

			continue
		}

		store.Sync(ctx, activeInstances, n.ID)

		syncRetrySuccess = true

		break
	}

	if !syncRetrySuccess {
		logger.L().Error(ctx, "Failed to sync node after max retries, temporarily marking as unhealthy", logger.WithNodeID(n.ID))
		n.setStatus(ctx, api.NodeStatusUnhealthy)

		return
	}

	builds, buildsErr := n.listCachedBuilds(ctx)
	if buildsErr != nil {
		logger.L().Error(ctx, "Error listing cached builds", zap.Error(buildsErr), logger.WithNodeID(n.ID))

		return
	}

	n.SyncBuilds(builds)
}
