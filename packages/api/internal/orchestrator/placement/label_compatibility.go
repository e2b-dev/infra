package placement

import (
	"github.com/e2b-dev/infra/packages/api/internal/orchestrator/nodemanager"
)

// isNodeLabelsCompatible checks if a node is compatible with the required scheduling labels.
//
// Matching rules (subset with dedicated pool protection):
//   - Both empty → match (default pool)
//   - Node has labels, sandbox has none → reject (protect dedicated nodes)
//   - Sandbox requires labels, node has none → reject (can't satisfy)
//   - Both have labels → required must be a subset of node labels
func isNodeLabelsCompatible(node *nodemanager.Node, requiredLabels []string) bool {
	nodeLabels := node.Labels()

	// Both empty → default pool match
	if len(requiredLabels) == 0 && len(nodeLabels) == 0 {
		return true
	}

	// Dedicated node protection: don't place unlabeled sandboxes on labeled nodes
	if len(requiredLabels) == 0 && len(nodeLabels) > 0 {
		return false
	}

	// Can't satisfy label requirements on an unlabeled node
	if len(requiredLabels) > 0 && len(nodeLabels) == 0 {
		return false
	}

	// Subset check: all required labels must be present on the node
	nodeLabelsSet := make(map[string]struct{}, len(nodeLabels))
	for _, l := range nodeLabels {
		nodeLabelsSet[l] = struct{}{}
	}

	for _, required := range requiredLabels {
		if _, ok := nodeLabelsSet[required]; !ok {
			return false
		}
	}

	return true
}
