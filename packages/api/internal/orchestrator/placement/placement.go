package placement

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/e2b-dev/infra/packages/api/internal/orchestrator/nodemanager"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/machineinfo"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

var tracer = otel.Tracer("github.com/e2b-dev/infra/packages/api/internal/orchestrator/placement")

var errSandboxCreateFailed = fmt.Errorf("failed to create a new sandbox, if the problem persists, contact us")

// Algorithm defines the interface for sandbox placement strategies.
// Implementations should choose an optimal node based on available resources
// and current load distribution.
type Algorithm interface {
	chooseNode(ctx context.Context, nodes []*nodemanager.Node, nodesExcluded map[string]struct{}, requested nodemanager.SandboxResources, buildMachineInfo machineinfo.MachineInfo) (*nodemanager.Node, error)
}

func PlaceSandbox(ctx context.Context, algorithm Algorithm, clusterNodes []*nodemanager.Node, preferredNode *nodemanager.Node, sbxRequest *orchestrator.SandboxCreateRequest, buildMachineInfo machineinfo.MachineInfo) (*nodemanager.Node, error) {
	ctx, span := tracer.Start(ctx, "place-sandbox")
	defer span.End()

	nodesExcluded := make(map[string]struct{})
	var err error

	var node *nodemanager.Node
	if preferredNode != nil {
		node = preferredNode
	}

	attempt := 0
	for attempt < maxRetries {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("request timed out during %d. attempt", attempt+1)
		default:
			// Continue
		}

		if node != nil {
			telemetry.ReportEvent(ctx, "Placing sandbox on the preferred node", telemetry.WithNodeID(node.ID))
		} else {
			if len(nodesExcluded) >= len(clusterNodes) {
				return nil, fmt.Errorf("no nodes available")
			}

			node, err = algorithm.chooseNode(ctx, clusterNodes, nodesExcluded, nodemanager.SandboxResources{CPUs: sbxRequest.GetSandbox().GetVcpu(), MiBMemory: sbxRequest.GetSandbox().GetRamMb()}, buildMachineInfo)
			if err != nil {
				return nil, err
			}

			telemetry.ReportEvent(ctx, "Placing sandbox on the node", telemetry.WithNodeID(node.ID))
		}

		node.PlacementMetrics.StartPlacing(sbxRequest.GetSandbox().GetSandboxId(), nodemanager.SandboxResources{
			CPUs:      sbxRequest.GetSandbox().GetVcpu(),
			MiBMemory: sbxRequest.GetSandbox().GetRamMb(),
		})

		ctx, span := tracer.Start(ctx, "create-sandbox")
		span.SetAttributes(
			telemetry.WithNodeID(node.ID),
			telemetry.WithClusterID(node.ClusterID),
		)
		err = node.SandboxCreate(ctx, sbxRequest)
		span.End()
		if err != nil {
			failedNode := node
			node = nil

			st, ok := status.FromError(err)
			if ok {
				switch st.Code() {
				case codes.ResourceExhausted:
					failedNode.PlacementMetrics.Skip(sbxRequest.GetSandbox().GetSandboxId())
					logger.L().Warn(ctx, "Node exhausted, trying another node", logger.WithSandboxID(sbxRequest.GetSandbox().GetSandboxId()), logger.WithNodeID(failedNode.ID))
				case codes.FailedPrecondition:
					failedNode.PlacementMetrics.Skip(sbxRequest.GetSandbox().GetSandboxId())
					logger.L().Warn(ctx, "Build not found, retrying", logger.WithSandboxID(sbxRequest.GetSandbox().GetSandboxId()), logger.WithNodeID(failedNode.ID))

					// We tried non-preferred node and the data aren't uploaded yet, try to use the preferred again
					// This should prevent spamming the preferred node, yet still try to place the sandbox there as it will be faster
					if preferredNode != nil &&
						preferredNode.ID != failedNode.ID {
						// Use the preferred node only if it wasn't excluded
						if _, excluded := nodesExcluded[preferredNode.ID]; !excluded {
							node = preferredNode
						}
					}
				default:
					nodesExcluded[failedNode.ID] = struct{}{}
					failedNode.PlacementMetrics.Fail(sbxRequest.GetSandbox().GetSandboxId())
					logger.L().Error(ctx, "Failed to create sandbox", logger.WithSandboxID(sbxRequest.GetSandbox().GetSandboxId()), logger.WithNodeID(failedNode.ID), zap.Int("attempt", attempt+1), zap.Error(utils.UnwrapGRPCError(err)))
					attempt++
				}

				continue
			}

			// Unexpected error
			nodesExcluded[failedNode.ID] = struct{}{}
			failedNode.PlacementMetrics.Fail(sbxRequest.GetSandbox().GetSandboxId())
			logger.L().Error(ctx, "Failed to create sandbox", logger.WithSandboxID(sbxRequest.GetSandbox().GetSandboxId()), logger.WithNodeID(failedNode.ID), zap.Int("attempt", attempt+1), zap.Error(err))
			attempt++

			continue
		}

		node.PlacementMetrics.Success(sbxRequest.GetSandbox().GetSandboxId())

		return node, nil
	}

	return nil, errSandboxCreateFailed
}
