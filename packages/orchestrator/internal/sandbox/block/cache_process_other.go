//go:build !linux

package block

import (
	"context"
	"fmt"
)

func (c *Cache) copyProcessMemory(
	_ context.Context,
	_ int,
	_ []Range,
) error {
	return fmt.Errorf("copying process memory is only supported on Linux")
}

func splitOversizedRanges(ranges []Range, maxSize int64) (result []Range) {
	for _, r := range ranges {
		if r.Size <= maxSize {
			result = append(result, r)

			continue
		}

		for offset := int64(0); offset < r.Size; offset += maxSize {
			result = append(result, Range{
				Start: r.Start + offset,
				Size:  min(r.Size-offset, maxSize),
			})
		}
	}

	return result
}
