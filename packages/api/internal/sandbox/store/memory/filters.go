package memory

import (
	"slices"

	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
)

// applyFilter checks if a sandbox matches the filter criteria
func applyFilter(sbx sandbox.Sandbox, filter *sandbox.ItemsFilter) bool {
	if filter.TeamID != nil && sbx.TeamID != *filter.TeamID {
		return false
	}
	if filter.States != nil && !slices.Contains(*filter.States, sbx.State) {
		return false
	}
	if filter.IsExpired != nil && sbx.IsExpired() != *filter.IsExpired {
		return false
	}
	return true
}
