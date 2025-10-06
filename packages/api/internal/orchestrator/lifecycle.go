package orchestrator

import (
	"context"
	"time"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	e2bcatalog "github.com/e2b-dev/infra/packages/shared/pkg/sandbox-catalog"
)

func (o *Orchestrator) observeTeamSandbox(ctx context.Context, sandbox sandbox.Sandbox, created bool) {
	o.teamMetricsObserver.Add(ctx, sandbox.TeamID, created)
}

func (o *Orchestrator) addToNode(ctx context.Context, sandbox sandbox.Sandbox, _ bool) {
	node := o.GetNode(sandbox.ClusterID, sandbox.NodeID)
	if node == nil {
		zap.L().Error("failed to get node", logger.WithNodeID(sandbox.NodeID))
	} else {
		node.AddSandbox(sandbox)

		info := e2bcatalog.SandboxInfo{
			OrchestratorID: node.Metadata().ServiceInstanceID,
			OrchestratorIP: node.IPAddress,
			ExecutionID:    sandbox.ExecutionID,

			SandboxStartedAt:        sandbox.StartTime,
			SandboxMaxLengthInHours: int64(sandbox.MaxInstanceLength / time.Hour),
		}

		o.dns.Add(ctx, sandbox.SandboxID, info)
	}
}
