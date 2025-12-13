package sandbox

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSandboxFactory(t *testing.T) {
	t.Run("wait respects context cancellation", func(t *testing.T) {
		var f Factory

		// simulate a long running sandbox
		f.wg.Add(1)

		// create a context that gets canceled after 1 second
		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			time.Sleep(time.Second)
			cancel()
		}()

		// wait in a goroutine, so we can get feedback without waiting 1 minute
		done := make(chan error)
		go func() {
			defer close(done)
			done <- f.Wait(ctx)
		}()

		// we should get an error in 1 second
		select {
		case err := <-done:
			require.ErrorIs(t, err, context.Canceled)
		case <-time.After(3 * time.Second):
			assert.Fail(t, "waitgroup didn't return in a reasonable time")
		}
	})
}
