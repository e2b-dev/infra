package placement

import (
	"context"

	"github.com/e2b-dev/infra/packages/api/internal/orchestrator/nodes"
)

// Algorithm defines the interface for sandbox placement strategies.
// Implementations should choose an optimal node based on available resources
// and current load distribution.
type Algorithm interface {
	ChooseNode(ctx context.Context, nodes []*nodes.Node, nodesExcluded map[string]struct{}, requested nodes.SandboxResources) (*nodes.Node, error)
}
