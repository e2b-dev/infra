package socket

import (
	"context"
	"errors"
	"os"
	"time"
)

const waitForSocketInterval = 10 * time.Millisecond

// Wait waits for the given file to exist.
func Wait(ctx context.Context, socketPath string) error {
	ticker := time.NewTicker(waitForSocketInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return errors.Join(ctx.Err(), context.Cause(ctx))
		case <-ticker.C:
			if _, err := os.Stat(socketPath); err != nil {
				continue
			}

			return nil
		}
	}
}
