package orchestrator

import (
	"context"
	"fmt"
	"slices"
	"sync"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox/sandboxtypes"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

// sandboxNodeLister is satisfied by *nodemanager.Node and any test double.
type sandboxNodeLister interface {
	GetSandboxes(ctx context.Context) ([]sandbox.Sandbox, error)
}

type namedLister struct {
	id     string
	lister sandboxNodeLister
}

// GetSandboxes returns instances for a given team filtered by states.
// It first queries Redis; if Redis is unavailable it falls back to querying
// each orchestrator node directly via gRPC. The fallback result may be
// slightly less consistent (a sandbox mid-migration could appear on two nodes
// briefly) but is far better than returning a 500 to the caller.
func (o *Orchestrator) GetSandboxes(ctx context.Context, teamID uuid.UUID, states []sandbox.State) ([]sandbox.Sandbox, error) {
	ctx, childSpan := tracer.Start(ctx, "get-sandboxes")
	defer childSpan.End()

	sandboxes, err := o.sandboxStore.TeamItems(ctx, teamID, states)
	if err == nil {
		return sandboxes, nil
	}

	logger.L().Warn(ctx, "Redis unavailable for sandbox listing, falling back to node gRPC",
		zap.Error(err),
		logger.WithTeamID(teamID.String()),
	)

	nodeMap := o.nodes.Items()
	listers := make([]namedLister, 0, len(nodeMap))
	for id, n := range nodeMap {
		listers = append(listers, namedLister{id: id, lister: n})
	}

	return getSandboxesFromNodes(ctx, listers, teamID, states)
}

// getSandboxesFromNodes fans out gRPC Sandbox.List calls to all provided nodes
// in parallel, filters by teamID and states, and deduplicates by sandboxID (a
// sandbox mid-migration may transiently appear on two nodes).
//
// Per-node errors are logged and skipped. If every node fails, an error is
// returned.
func getSandboxesFromNodes(ctx context.Context, nodes []namedLister, teamID uuid.UUID, states []sandboxtypes.State) ([]sandbox.Sandbox, error) {
	if len(nodes) == 0 {
		return nil, fmt.Errorf("no orchestrator nodes connected")
	}

	type nodeResult struct {
		sandboxes []sandbox.Sandbox
		err       error
	}

	results := make([]nodeResult, len(nodes))
	var wg sync.WaitGroup

	for i, entry := range nodes {
		wg.Add(1)
		go func(idx int, e namedLister) {
			defer wg.Done()
			sbxs, err := e.lister.GetSandboxes(ctx)
			results[idx] = nodeResult{sandboxes: sbxs, err: err}
		}(i, entry)
	}
	wg.Wait()

	seen := make(map[string]struct{})
	var out []sandbox.Sandbox
	var nodeErrors int

	for i, r := range results {
		if r.err != nil {
			nodeErrors++
			logger.L().Warn(ctx, "Node gRPC fallback: failed to list sandboxes from node",
				zap.Error(r.err),
				logger.WithNodeID(nodes[i].id),
			)

			continue
		}

		for _, sbx := range r.sandboxes {
			if sbx.TeamID != teamID {
				continue
			}
			if len(states) > 0 && !slices.Contains(states, sbx.State) {
				continue
			}
			if _, ok := seen[sbx.SandboxID]; ok {
				continue
			}
			seen[sbx.SandboxID] = struct{}{}
			out = append(out, sbx)
		}
	}

	if nodeErrors == len(nodes) {
		return nil, fmt.Errorf("all %d orchestrator nodes failed to respond during Redis fallback", len(nodes))
	}

	return out, nil
}
