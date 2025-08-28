package placement

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/e2b-dev/infra/packages/api/internal/orchestrator/nodemanager"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

var errSandboxCreateFailed = fmt.Errorf("failed to create a new sandbox, if the problem persists, contact us")

// Algorithm defines the interface for sandbox placement strategies.
// Implementations should choose an optimal node based on available resources
// and current load distribution.
type Algorithm interface {
	chooseNode(ctx context.Context, nodes []*nodemanager.Node, nodesExcluded map[string]struct{}, requested nodemanager.SandboxResources) (*nodemanager.Node, error)
}

func PlaceSandbox(ctx context.Context, tracer trace.Tracer, algorithm Algorithm, clusterNodes []*nodemanager.Node, preferredNode *nodemanager.Node, sbxRequest *orchestrator.SandboxCreateRequest) (*nodemanager.Node, error) {
	ctx, span := tracer.Start(ctx, "place-sandbox")
	defer span.End()

	nodesExcluded := make(map[string]struct{})
	var err error

	attempt := 0
	for attempt < maxRetries {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("request timed out during %d. attempt", attempt+1)
		default:
			// Continue
		}

		var node *nodemanager.Node
		if preferredNode != nil {
			node = preferredNode
			telemetry.ReportEvent(ctx, "Placing sandbox on the preferred node", telemetry.WithNodeID(node.ID))
		} else {
			if len(nodesExcluded) >= len(clusterNodes) {
				return nil, fmt.Errorf("no nodes available")
			}

			childCtx, childSpan := tracer.Start(ctx, "choose-node")
			node, err = algorithm.chooseNode(childCtx, clusterNodes, nodesExcluded, nodemanager.SandboxResources{CPUs: sbxRequest.Sandbox.Vcpu, MiBMemory: sbxRequest.Sandbox.RamMb})
			childSpan.End()
			if err != nil {
				return nil, err
			}

			telemetry.ReportEvent(ctx, "Placing sandbox on the node", telemetry.WithNodeID(node.ID))
		}

		node.PlacementMetrics.StartPlacing(sbxRequest.Sandbox.SandboxId, nodemanager.SandboxResources{
			CPUs:      sbxRequest.Sandbox.Vcpu,
			MiBMemory: sbxRequest.Sandbox.RamMb,
		})

		ctx, span := tracer.Start(ctx, "create-sandbox")
		span.SetAttributes(
			telemetry.WithNodeID(node.ID),
			telemetry.WithClusterID(node.ClusterID),
		)
		err = node.SandboxCreate(ctx, sbxRequest)
		span.End()
		if err != nil {
			nodesExcluded[node.ID] = struct{}{}

			st, ok := status.FromError(err)
			if !ok || st.Code() != codes.ResourceExhausted {
				node.PlacementMetrics.Fail(sbxRequest.Sandbox.SandboxId)
				zap.L().Error("Failed to create sandbox", logger.WithSandboxID(sbxRequest.Sandbox.SandboxId), logger.WithNodeID(node.ID), zap.Int("attempt", attempt+1), zap.Error(utils.UnwrapGRPCError(err)))
				attempt++
			} else {
				zap.L().Warn("Node exhausted, trying another node", logger.WithSandboxID(sbxRequest.Sandbox.SandboxId), logger.WithNodeID(node.ID))
			}

			node = nil

			continue
		}

		node.PlacementMetrics.Success(sbxRequest.Sandbox.SandboxId)
		return node, nil
	}

	return nil, errSandboxCreateFailed
}
