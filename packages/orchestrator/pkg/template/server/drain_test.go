//go:build linux

package server

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

func TestWaitReturnsErrorWhileBuildsInFlight(t *testing.T) {
	t.Parallel()

	s := &ServerStore{
		logger: logger.NewNopLogger(),
		wg:     &sync.WaitGroup{},
	}
	s.wg.Add(1)
	defer s.wg.Done()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	require.ErrorIs(t, s.Wait(ctx), context.Canceled)
}

func TestWaitStopsAtContextDeadlineDuringGracePeriod(t *testing.T) {
	t.Parallel()

	s := &ServerStore{
		logger: logger.NewNopLogger(),
		wg:     &sync.WaitGroup{},
	}

	// No builds in flight, so the wait passes straight to the consumer grace
	// period, which must be bounded by ctx rather than the full sleep.
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()

	require.ErrorIs(t, s.Wait(ctx), context.DeadlineExceeded)
}
