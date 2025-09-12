package orchestrator

import (
	"context"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/cache/instance"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

func (o *Orchestrator) observeTeamSandbox(ctx context.Context, sandbox instance.Data, created bool) {
	o.teamMetricsObserver.Add(ctx, sandbox.TeamID, created)
}

func (o *Orchestrator) addToNode(ctx context.Context, sandbox instance.Data, _ bool) {
	node := o.GetNode(sandbox.ClusterID, sandbox.NodeID)
	if node == nil {
		zap.L().Error("failed to get node", logger.WithNodeID(sandbox.NodeID))
	} else {
		node.AddSandbox(sandbox)

		o.dns.Add(ctx, sandbox.SandboxID, node.IPAddress)
	}
}
