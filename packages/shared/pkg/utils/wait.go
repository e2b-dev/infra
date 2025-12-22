package utils

import (
	"context"
	"sync"
)

func Wait(ctx context.Context, wg *sync.WaitGroup) error {
	if err := ctx.Err(); err != nil {
		return ctx.Err()
	}

	done := make(chan struct{}, 1)

	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		return nil
	}
}
