package placement

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"time"

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

// Algorithm defines the interface for sandbox placement strategies.
// Implementations should choose an optimal node based on available resources
// and current load distribution.
type Algorithm interface {
	chooseNode(ctx context.Context, nodes []*nodemanager.Node, nodesExcluded map[string]struct{}, requested nodemanager.SandboxResources, buildMachineInfo machineinfo.MachineInfo, filterByLabels bool, requiredLabels []string) (*nodemanager.Node, error)
}

func PlaceSandbox(ctx context.Context, algorithm Algorithm, clusterNodes []*nodemanager.Node, preferredNode *nodemanager.Node, sbxRequest *orchestrator.SandboxCreateRequest, buildMachineInfo machineinfo.MachineInfo, labelFilteringEnabled bool, requiredLabels []string) (*nodemanager.Node, error) {
	ctx, span := tracer.Start(ctx, "place-sandbox")
	defer span.End()

	// nodesExcluded: nodes that hard-failed and must not be retried.
	// nodesExhausted: nodes that reported ResourceExhausted; skipped so other
	// nodes are tried first, then retried after a bounded backoff.
	nodesExcluded := make(map[string]struct{})
	nodesExhausted := make(map[string]struct{})
	var err error

	var node *nodemanager.Node
	if preferredNode != nil {
		node = preferredNode
	}

	attempt := 0
	exhaustedRetries := 0
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
				return nil, errors.New("no nodes available")
			}

			skip := make(map[string]struct{}, len(nodesExcluded)+len(nodesExhausted))
			for id := range nodesExcluded {
				skip[id] = struct{}{}
			}
			for id := range nodesExhausted {
				skip[id] = struct{}{}
			}

			node, err = algorithm.chooseNode(ctx, clusterNodes, skip, nodemanager.SandboxResources{CPUs: sbxRequest.GetSandbox().GetVcpu(), MiBMemory: sbxRequest.GetSandbox().GetRamMb()}, buildMachineInfo, labelFilteringEnabled, requiredLabels)
			if err != nil {
				// No selectable node. If exhausted nodes are the only blocker,
				// back off (bounded) and retry them; otherwise give up.
				if len(nodesExhausted) == 0 {
					return nil, err
				}

				exhaustedRetries++
				if exhaustedRetries >= maxResourceExhaustedRetries {
					logger.L().Warn(ctx, "Placement failed, all nodes exhausted", logger.WithSandboxID(sbxRequest.GetSandbox().GetSandboxId()), logger.WithTemplateID(sbxRequest.GetSandbox().GetTemplateId()), logger.WithBuildID(sbxRequest.GetSandbox().GetBuildId()), zap.Int("exhausted_retries", exhaustedRetries))

					return nil, errSandboxCreateFailed
				}

				select {
				case <-ctx.Done():
					return nil, fmt.Errorf("request timed out after %d exhausted retries", exhaustedRetries)
				case <-time.After(exhaustedBackoff(exhaustedRetries)):
				}

				nodesExhausted = make(map[string]struct{})

				continue
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

			return node, nil
		}

		failedNode := node
		node = nil

		st, ok := status.FromError(err)
		statusCode := codes.Internal
		if ok {
			statusCode = st.Code()
		}

		switch statusCode {
		case codes.ResourceExhausted:
			failedNode.PlacementMetrics.Skip(sbxRequest.GetSandbox().GetSandboxId())
			nodesExhausted[failedNode.ID] = struct{}{}
		default:
			nodesExcluded[failedNode.ID] = struct{}{}
			failedNode.PlacementMetrics.Fail(sbxRequest.GetSandbox().GetSandboxId())
			logger.L().Error(ctx, "Failed to create sandbox",
				logger.WithSandboxID(sbxRequest.GetSandbox().GetSandboxId()),
				logger.WithNodeID(failedNode.ID),
				logger.WithTemplateID(sbxRequest.GetSandbox().GetTemplateId()),
				logger.WithBuildID(sbxRequest.GetSandbox().GetBuildId()),
				zap.Int("attempt", attempt+1),
				zap.Error(utils.UnwrapGRPCError(err)),
			)
			attempt++
		}
	}

	return nil, errSandboxCreateFailed
}

// exhaustedBackoff returns a jittered, exponentially growing wait for the n-th
// (1-based) ResourceExhausted retry, capped at resourceExhaustedBackoffMaxWait.
func exhaustedBackoff(n int) time.Duration {
	wait := resourceExhaustedBackoffBase << (n - 1)
	if wait <= 0 || wait > resourceExhaustedBackoffMaxWait {
		wait = resourceExhaustedBackoffMaxWait
	}

	return time.Duration(rand.Int63n(int64(wait)) + 1)
}
