package orchestrator

import (
	"context"
	"time"

	"go.uber.org/zap"

	orchestratorcatalog "github.com/e2b-dev/infra/packages/api/internal/orchestrator/catalog"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/env"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	e2bcatalog "github.com/e2b-dev/infra/packages/shared/pkg/sandbox-catalog"
)

func (o *Orchestrator) addSandboxToRoutingTable(ctx context.Context, sandbox sandbox.Sandbox) {
	node := o.GetNode(sandbox.ClusterID, sandbox.NodeID)
	if node == nil {
		logger.L().Error(ctx, "failed to get node", logger.WithNodeID(sandbox.NodeID))

		return
	}

	// Only add to routing table if the node is managed by Nomad
	// For remote cluster nodes we are using gPRC metadata for routing registration instead
	if !node.IsNomadManaged() && !env.IsLocal() {
		return
	}

	nodeIP := routeNodeIPAddress(node, env.IsLocal())

	info := e2bcatalog.SandboxInfo{
		TeamID:         sandbox.TeamID.String(),
		OrchestratorID: node.Metadata().ServiceInstanceID,
		OrchestratorIP: nodeIP,

		ExecutionID:      sandbox.ExecutionID,
		StartedAt:        sandbox.StartTime,
		MaxLengthInHours: int64(sandbox.MaxInstanceLength / time.Hour),
		Keepalive:        orchestratorcatalog.KeepaliveFromDB(sandbox.Keepalive),
	}

	lifetime := time.Until(sandbox.StartTime.Add(sandbox.MaxInstanceLength))
	if lifetime <= 0 {
		logger.L().Warn(ctx, "skipping sandbox routing info with expired lifetime", logger.WithSandboxID(sandbox.SandboxID), zap.Duration("lifetime", lifetime))

		return
	}

	err := o.routingCatalog.StoreSandbox(ctx, sandbox.SandboxID, &info, lifetime)
	if err != nil {
		logger.L().Error(ctx, "error adding routing record to catalog", zap.Error(err), logger.WithSandboxID(sandbox.SandboxID))
	}
}
