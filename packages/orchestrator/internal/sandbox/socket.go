package sandbox

import (
	"context"
	"os"
	"time"
)

// waitForSocket waits for the given file to exist.
func waitForSocket(ctx context.Context, socketPath string) error {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if _, err := os.Stat(socketPath); err != nil {
				continue
			}

			// TODO: Send test HTTP request to make sure socket is available
			return nil
		}
	}
}
