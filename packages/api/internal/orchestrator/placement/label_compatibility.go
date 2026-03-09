package placement

import (
	"github.com/e2b-dev/infra/packages/api/internal/orchestrator/nodemanager"
)

const defaultLabel = "default"

// effectiveNodeLabels returns the node's labels, defaulting to ["default"] if empty.
func effectiveNodeLabels(nodeLabels []string) []string {
	if len(nodeLabels) == 0 {
		return []string{defaultLabel}
	}

	return nodeLabels
}

// effectiveSandboxLabels returns the sandbox's required labels, defaulting to ["default"] if empty.
func effectiveSandboxLabels(requiredLabels []string) []string {
	if len(requiredLabels) == 0 {
		return []string{defaultLabel}
	}

	return requiredLabels
}

// isNodeLabelsCompatible checks if a node is compatible with the required scheduling labels.
// Empty labels on either side are normalized to ["default"] before comparison.
// After normalization, all required labels must be a subset of node labels.
func isNodeLabelsCompatible(node *nodemanager.Node, requiredLabels []string) bool {
	nodeLabels := effectiveNodeLabels(node.Labels())
	required := effectiveSandboxLabels(requiredLabels)

	nodeLabelsSet := make(map[string]struct{}, len(nodeLabels))
	for _, l := range nodeLabels {
		nodeLabelsSet[l] = struct{}{}
	}

	for _, req := range required {
		if _, ok := nodeLabelsSet[req]; !ok {
			return false
		}
	}

	return true
}
