package supabaseauthusersync

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

func TestSuperviseRestartsAfterUnexpectedError(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var runs atomic.Int32
	errCh := make(chan error, 1)

	go func() {
		errCh <- supervise(ctx, logger.NewNopLogger(), supervisorConfig{
			RestartDelay:         time.Millisecond,
			MaxRestartDelay:      time.Millisecond,
			HealthyRunResetAfter: time.Hour,
		}, func(ctx context.Context) error {
			attempt := runs.Add(1)
			if attempt < 3 {
				return errors.New("boom")
			}

			cancel()
			<-ctx.Done()

			return ctx.Err()
		})
	}()

	err := <-errCh
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, int32(3), runs.Load())
}

func TestSuperviseRestartsAfterPanic(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var runs atomic.Int32
	errCh := make(chan error, 1)

	go func() {
		errCh <- supervise(ctx, logger.NewNopLogger(), supervisorConfig{
			RestartDelay:         time.Millisecond,
			MaxRestartDelay:      time.Millisecond,
			HealthyRunResetAfter: time.Hour,
		}, func(ctx context.Context) error {
			attempt := runs.Add(1)
			if attempt == 1 {
				panic("boom")
			}

			cancel()
			<-ctx.Done()

			return ctx.Err()
		})
	}()

	err := <-errCh
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, int32(2), runs.Load())
}
