package socket

import (
	"context"
	"fmt"
	"os"
	"time"
)

const waitInterval = 10 * time.Millisecond

// Wait waits for the given file to exist.
func Wait(ctx context.Context, socketPath string) error {
	ticker := time.NewTicker(waitInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("cancelled wait for socket '%s': %w", socketPath, ctx.Err())
		case <-ticker.C:
			if _, err := os.Stat(socketPath); err != nil {
				continue
			}

			return nil
		}
	}
}
