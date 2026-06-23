package placement

import (
	"context"
	"errors"
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

var errSandboxCreateFailed = errors.New("failed to create a new sandbox, if the problem persists, contact us")

// PlacementResult carries the outcome of a placement attempt alongside the error.
type PlacementResult struct {
	// Node is the node the sandbox was placed on, or nil on failure.
	Node *nodemanager.Node
	// WarmedNode is the first node that attempted the create before a timeout,
	// whose cache is warmest; nil unless TimedOut.
	WarmedNode *nodemanager.Node
	// TimedOut reports whether placement failed due to context cancellation/deadline.
	TimedOut bool
}

// Algorithm defines the interface for sandbox placement strategies.
// Implementations should choose an optimal node based on available resources
// and current load distribution.
type Algorithm interface {
	chooseNode(ctx context.Context, nodes []*nodemanager.Node, nodesExcluded map[string]struct{}, requested nodemanager.SandboxResources, buildMachineInfo machineinfo.MachineInfo, filterByLabels bool, requiredLabels []string) (*nodemanager.Node, error)
}

func PlaceSandbox(
	ctx context.Context,
	algorithm Algorithm,
	clusterNodes []*nodemanager.Node,
	preferredNode *nodemanager.Node,
	sbxRequest *orchestrator.SandboxCreateRequest,
	buildMachineInfo machineinfo.MachineInfo,
	labelFilteringEnabled bool,
	requiredLabels []string,
) (PlacementResult, error) {
	ctx, span := tracer.Start(ctx, "place-sandbox")
	defer span.End()

	nodesExcluded := make(map[string]struct{})
	var err error

	var node *nodemanager.Node
	if preferredNode != nil {
		node = preferredNode
	}

	// First node that attempted the create (not a fast ResourceExhausted refusal).
	var firstTriedNode *nodemanager.Node

	// failed reports the warming node only when the failure was caused by the
	// request context being cancelled or timing out (ctx.Err() != nil). Hard
	// failures (where the context is still live) carry no node, so callers never
	// pin a retry to a node that genuinely refused the sandbox.
	//
	// TODO [EN-1099]: We key off ctx.Err() rather than the gRPC status code because
	// the orchestrator currently collapses a timed-out resume into codes.Internal
	// (it folds the deadline cause into the message, not the code),
	// so the code alone cannot tell a timeout apart from a hard failure.
	failed := func(err error) (PlacementResult, error) {
		if ctx.Err() == nil {
			return PlacementResult{}, err
		}

		return PlacementResult{WarmedNode: firstTriedNode, TimedOut: true}, err
	}

	attempt := 0
	for attempt < maxRetries {
		select {
		case <-ctx.Done():
			return failed(fmt.Errorf("request timed out during %d. attempt", attempt+1))
		default:
			// Continue
		}

		if node != nil {
			telemetry.ReportEvent(ctx, "Placing sandbox on the preferred node", telemetry.WithNodeID(node.ID))
		} else {
			if len(nodesExcluded) >= len(clusterNodes) {
				return failed(errors.New("no nodes available"))
			}

			node, err = algorithm.chooseNode(ctx, clusterNodes, nodesExcluded, nodemanager.SandboxResources{CPUs: sbxRequest.GetSandbox().GetVcpu(), MiBMemory: sbxRequest.GetSandbox().GetRamMb()}, buildMachineInfo, labelFilteringEnabled, requiredLabels)
			if err != nil {
				return failed(err)
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
		if err == nil {
			node.PlacementMetrics.Success(sbxRequest.GetSandbox().GetSandboxId())

			// Optimistic update: assume resources are occupied after successful creation.
			// Manually update node.metrics with the newly allocated resources.
			// This will be overwritten by the next real Metrics report for auto-correction.
			node.OptimisticAdd(ctx, nodemanager.SandboxResources{
				CPUs:      sbxRequest.GetSandbox().GetVcpu(),
				MiBMemory: sbxRequest.GetSandbox().GetRamMb(),
			})

			return PlacementResult{Node: node}, nil
		}

		failedNode := node
		node = nil

		st, ok := status.FromError(err)
		statusCode := codes.Internal
		if ok {
			statusCode = st.Code()
		}

		// Remember the first node that got far enough to actually attempt the
		// sandbox (i.e. did not refuse with ResourceExhausted).
		if statusCode != codes.ResourceExhausted && firstTriedNode == nil {
			firstTriedNode = failedNode
		}

		switch statusCode {
		case codes.ResourceExhausted:
			failedNode.PlacementMetrics.Skip(sbxRequest.GetSandbox().GetSandboxId())
			logger.L().Warn(ctx, "Node exhausted, trying another node", logger.WithSandboxID(sbxRequest.GetSandbox().GetSandboxId()), logger.WithNodeID(failedNode.ID))
		default:
			nodesExcluded[failedNode.ID] = struct{}{}
			failedNode.PlacementMetrics.Fail(sbxRequest.GetSandbox().GetSandboxId())
			logger.L().Error(ctx, "Failed to create sandbox", logger.WithSandboxID(sbxRequest.GetSandbox().GetSandboxId()), logger.WithNodeID(failedNode.ID), zap.Int("attempt", attempt+1), zap.Error(utils.UnwrapGRPCError(err)))
			attempt++
		}
	}

	return failed(errSandboxCreateFailed)
}
