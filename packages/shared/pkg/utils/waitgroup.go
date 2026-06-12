package utils

import (
	"context"
	"fmt"
	"runtime"
	"sync"
)

func WaitGroupWait(ctx context.Context, wg *sync.WaitGroup) error {
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	for range 10 {
		select {
		case <-done:
			return nil
		default:
			runtime.Gosched()
		}
	}

	select {
	case <-ctx.Done():
		return fmt.Errorf("waiting for wait group: %w", ctx.Err())
	case <-done:
		return nil
	}
}
