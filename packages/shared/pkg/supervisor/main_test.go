package supervisor

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var ErrSignal = errors.New("oh noes")

func TestHappyPath(t *testing.T) {
	// create supervisor
	s := New()

	// setup
	var counter int
	var cleanup bool
	s.AddTask("run something in the background",
		WithCleanup(func(ctx context.Context) error {
			cleanup = true
			return nil
		}),
		WithBackgroundJob(func(ctx context.Context) error {
			ticker := time.Tick(200 * time.Millisecond)

			for {
				select {
				case <-ticker:
					counter++
				case <-ctx.Done():
					return ErrSignal
				}
			}
		}))

	// run tasks for 500 ms
	ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
	t.Cleanup(cancel)

	err := s.Run(ctx)
	require.ErrorIs(t, err, context.DeadlineExceeded)

	// verify that the task only ran twice
	assert.Equal(t, 2, counter)
	assert.False(t, cleanup)

	// clean up
	err = s.Close(ctx)
	require.NoError(t, err)

	// verify that the cleanup function was called
	assert.True(t, cleanup)
}
