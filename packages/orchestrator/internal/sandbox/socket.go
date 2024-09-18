package sandbox

import (
	"context"
	"os"
	"time"
)

const (
	waitForSocketInterval = 10 * time.Millisecond
)

// waitForSocket waits for the given file to exist.
func waitForSocket(ctx context.Context, socketPath string) error {
	ticker := time.NewTicker(waitForSocketInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if _, err := os.Stat(socketPath); err != nil {
				continue
			}

			return nil
		}
	}
}
