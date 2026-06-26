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

	return waitDoneOrContext(ctx, done)
}

// waitDoneOrContext blocks until done is closed or ctx is cancelled.
//
// When both are ready, select chooses pseudo-randomly, so a re-check biases the
// result toward done: a closed done channel means the wait group has actually
// finished, which is a definitive success and must win over the context error.
func waitDoneOrContext(ctx context.Context, done <-chan struct{}) error {
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		select {
		case <-done:
			return nil
		default:
			return fmt.Errorf("waiting for wait group: %w", ctx.Err())
		}
	}
}
