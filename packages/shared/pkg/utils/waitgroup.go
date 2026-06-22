package utils

import (
	"context"
	"fmt"
	"sync"
)

func WaitGroupWait(ctx context.Context, wg *sync.WaitGroup) error {
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("waiting for wait group: %w", ctx.Err())
	}
}
