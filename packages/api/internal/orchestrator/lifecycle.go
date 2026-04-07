package orchestrator

import (
	"context"
	"time"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/env"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	e2bcatalog "github.com/e2b-dev/infra/packages/shared/pkg/sandbox-catalog"
)

func getRoutingCatalogTTL(now time.Time, sandbox sandbox.Sandbox) time.Duration {
	if sandbox.EndTime.After(now) {
		return sandbox.EndTime.Sub(now)
	}

	return time.Millisecond
}

func (o *Orchestrator) syncSandboxToRoutingCatalog(ctx context.Context, sandbox sandbox.Sandbox) {
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

	nodeIP := node.IPAddress

	info := e2bcatalog.SandboxInfo{
		OrchestratorID: node.Metadata().ServiceInstanceID,
		OrchestratorIP: nodeIP,

		ExecutionID:      sandbox.ExecutionID,
		StartedAt:        sandbox.StartTime,
		EndAt:            sandbox.EndTime,
		MaxLengthInHours: int64(sandbox.MaxInstanceLength / time.Hour),
	}

	lifetime := getRoutingCatalogTTL(time.Now(), sandbox)
	err := o.routingCatalog.StoreSandbox(ctx, sandbox.SandboxID, &info, lifetime)
	if err != nil {
		logger.L().Error(ctx, "error adding routing record to catalog", zap.Error(err), logger.WithSandboxID(sandbox.SandboxID))
	}
}

func (o *Orchestrator) addSandboxToRoutingTable(ctx context.Context, sandbox sandbox.Sandbox) {
	o.syncSandboxToRoutingCatalog(ctx, sandbox)
}
