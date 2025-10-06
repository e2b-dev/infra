package memory

import (
	"slices"

	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
)

// applyFilter checks if a sandbox matches the filter criteria
func applyFilter(sbx sandbox.Sandbox, filter *sandbox.ItemsFilter) bool {
	if filter.States != nil && !slices.Contains(filter.States, sbx.State) {
		return false
	}
	if filter.OnlyExpired && !sbx.IsExpired() {
		return false
	}
	return true
}
