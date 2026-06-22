package draingate

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestEnterReleaseAndWait(t *testing.T) {
	t.Parallel()

	g := New()
	release, err := g.Enter()
	require.NoError(t, err)

	waitDone := make(chan error, 1)
	go func() {
		waitDone <- g.Wait(t.Context())
	}()

	requireNotDone(t, waitDone)
	release()
	release()
	require.NoError(t, requireDone(t, waitDone))
}

func TestRejectAfterDrain(t *testing.T) {
	t.Parallel()

	g := New()
	require.True(t, g.StartDraining())
	require.False(t, g.StartDraining())
	require.True(t, g.Draining())

	select {
	case <-g.Done():
	default:
		t.Fatal("Done channel was not closed")
	}

	release, err := g.Enter()
	require.ErrorIs(t, err, ErrDraining)
	require.Nil(t, release)
}

func TestWaitBlocksUntilAllReleases(t *testing.T) {
	t.Parallel()

	g := New()
	release1, err := g.Enter()
	require.NoError(t, err)
	release2, err := g.Enter()
	require.NoError(t, err)

	g.StartDraining()
	waitDone := make(chan error, 1)
	go func() {
		waitDone <- g.Wait(t.Context())
	}()

	requireNotDone(t, waitDone)
	release1()
	requireNotDone(t, waitDone)
	release2()
	require.NoError(t, requireDone(t, waitDone))
}

func TestWaitReturnsContextError(t *testing.T) {
	t.Parallel()

	g := New()
	release, err := g.Enter()
	require.NoError(t, err)
	defer release()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	require.ErrorIs(t, g.Wait(ctx), context.Canceled)
}

func TestEnterDuringWaitWhileNotDrainingIsNotBlocked(t *testing.T) {
	t.Parallel()

	g := New()
	release, err := g.Enter()
	require.NoError(t, err)

	waitDone := make(chan error, 1)
	go func() {
		waitDone <- g.Wait(t.Context())
	}()
	requireNotDone(t, waitDone)

	entered := make(chan error, 1)
	go func() {
		secondRelease, err := g.Enter()
		if err == nil {
			secondRelease()
		}
		entered <- err
	}()

	require.NoError(t, requireDone(t, entered))
	release()
	require.NoError(t, requireDone(t, waitDone))
}

func TestConcurrentStress(t *testing.T) {
	t.Parallel()

	g := New()
	start := make(chan struct{})
	entered := make(chan func(), 100)
	rejected := make(chan error, 100)
	var wg sync.WaitGroup

	for range 100 {
		wg.Go(func() {
			<-start
			release, err := g.Enter()
			if err != nil {
				rejected <- err

				return
			}

			entered <- release
		})
	}

	close(start)
	require.Eventually(t, func() bool {
		return len(entered)+len(rejected) == 100
	}, time.Second, time.Millisecond)

	g.StartDraining()
	wg.Wait()
	close(entered)
	close(rejected)

	for err := range rejected {
		require.ErrorIs(t, err, ErrDraining)
	}

	waitDone := make(chan error, 1)
	go func() {
		waitDone <- g.Wait(t.Context())
	}()

	for release := range entered {
		release()
	}
	require.NoError(t, requireDone(t, waitDone))

	_, err := g.Enter()
	require.ErrorIs(t, err, ErrDraining)
}

func TestZeroValueAndNilGateAreSafe(t *testing.T) {
	t.Parallel()

	var g Gate
	release, err := g.Enter()
	require.NoError(t, err)
	release()
	require.NoError(t, g.Wait(t.Context()))
	require.True(t, g.StartDraining())
	require.True(t, g.Draining())

	var nilGate *Gate
	release, err = nilGate.Enter()
	require.NoError(t, err)
	release()
	require.NoError(t, nilGate.Wait(t.Context()))
	require.False(t, nilGate.StartDraining())
	require.False(t, nilGate.Draining())
	require.Nil(t, nilGate.Done())
}

func requireDone[T any](t *testing.T, ch <-chan T) T {
	t.Helper()

	select {
	case got := <-ch:
		return got
	case <-time.After(time.Second):
		t.Fatal("channel did not receive")

		var zero T

		return zero
	}
}

func requireNotDone[T any](t *testing.T, ch <-chan T) {
	t.Helper()

	select {
	case got := <-ch:
		t.Fatalf("channel received unexpectedly: %v", got)
	case <-time.After(25 * time.Millisecond):
	}
}
