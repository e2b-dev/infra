package optimize

import (
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
)

// computeCommonPrefetchEntries computes the intersection of multiple prefetch data sets.
// Only pages that appear in ALL runs are included.
// For common pages, it uses the average order and prefers "read" access type if any run differs.
func computeCommonPrefetchEntries(allData []block.PrefetchData) []block.PrefetchBlockEntry {
	if len(allData) == 0 {
		return nil
	}

	var commonEntries []block.PrefetchBlockEntry

	// Use the first run as the base and check against all other runs
	// This is a simple intersection algorithm.
	for idx, entry1 := range allData[0].BlockEntries {
		// Check if this index exists in all other runs
		totalOrder := entry1.Order
		accessType := entry1.AccessType
		allMatch := true

		for i := 1; i < len(allData); i++ {
			entry, exists := allData[i].BlockEntries[idx]
			if !exists {
				allMatch = false

				break
			}
			totalOrder += entry.Order
			// If any access type differs, prefer "read"
			if entry.AccessType != accessType {
				accessType = block.Read
			}
		}

		if !allMatch {
			continue
		}

		// Compute average order across all runs
		avgOrder := totalOrder / uint64(len(allData))

		commonEntries = append(commonEntries, block.PrefetchBlockEntry{
			Index:      idx,
			Order:      avgOrder,
			AccessType: accessType,
		})
	}

	return commonEntries
}
