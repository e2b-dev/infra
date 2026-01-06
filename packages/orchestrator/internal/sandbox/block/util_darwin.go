//go:build darwin

package block

import (
	"context"
	"fmt"
	"os"
)

// FileSize returns the size of the cache on disk.
// The size might differ from the dirty size, as it may not be fully on disk.
func (c *Cache) FileSize() (int64, error) {
	s, err := os.Stat(c.filePath)
	if err != nil {
		return 0, fmt.Errorf("failed to get file stats: %w", err)
	}

	return s.Size(), nil
}

func (c *Cache) copyProcessMemory(
	ctx context.Context,
	pid int,
	ranges []Range,
) error {
	return fmt.Errorf("not implemented on MacOS")
}
