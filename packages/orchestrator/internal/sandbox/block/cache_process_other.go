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
