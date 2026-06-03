//go:build linux

package server

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/cfg"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox"
)

var errFactoryStartEntered = errors.New("factory start entered")

func TestWaitSandboxStartsCanceledDoesNotBlockDrainingRejection(t *testing.T) {
	t.Parallel()

	s := &Server{done: make(chan struct{})}

	s.sandboxStartMu.RLock()
	defer s.sandboxStartMu.RUnlock()

	waitCtx, cancel := context.WithCancel(t.Context())
	waitErr := make(chan error, 1)
	go func() {
		waitErr <- s.waitSandboxStarts(waitCtx)
	}()

	// Give waitSandboxStarts a chance to observe the held read lock. The old
	// implementation left a queued writer here, which blocked future RLock calls.
	time.Sleep(2 * sandboxStartWaitPollInterval)
	cancel()

	select {
	case err := <-waitErr:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("waitSandboxStarts did not return after cancellation")
	}

	close(s.done)

	enterErr := make(chan error, 1)
	go func() {
		release, err := s.enterSandboxStart(t.Context(), "test")
		if err == nil {
			release()
		}

		enterErr <- err
	}()

	select {
	case err := <-enterErr:
		require.Equal(t, codes.Unavailable, status.Code(err))
	case <-time.After(time.Second):
		t.Fatal("enterSandboxStart blocked instead of rejecting while draining")
	}
}

func TestForceStopSandboxesWaitsForInFlightStarts(t *testing.T) {
	t.Parallel()

	s := forceStopTestServer()
	s.sandboxStartMu.RLock()
	locked := true
	defer func() {
		if locked {
			s.sandboxStartMu.RUnlock()
		}
	}()

	done := make(chan error, 1)
	go func() {
		done <- s.ForceStopSandboxes(t.Context())
	}()

	select {
	case err := <-done:
		require.Failf(t, "ForceStopSandboxes returned before start left", "err: %v", err)
	case <-time.After(2 * sandboxStartWaitPollInterval):
	}

	s.sandboxStartMu.RUnlock()
	locked = false

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("ForceStopSandboxes did not return after start left")
	}
}

func TestForceStopSandboxesReturnsInFlightStartContextError(t *testing.T) {
	t.Parallel()

	s := forceStopTestServer()
	s.sandboxStartMu.RLock()
	defer s.sandboxStartMu.RUnlock()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	require.ErrorIs(t, s.ForceStopSandboxes(ctx), context.Canceled)
}

func TestForceStopSandboxesUntilStartsFinishStopsLifecycleAfterWaitError(t *testing.T) {
	t.Parallel()

	lateSandbox := &sandbox.Sandbox{}
	var mu sync.Mutex
	tracked := []*sandbox.Sandbox{}
	stopped := 0

	firstForceStop := make(chan struct{})
	var firstForceStopOnce sync.Once
	releaseWait := make(chan struct{})

	lifecycleItems := func() []*sandbox.Sandbox {
		mu.Lock()
		defer mu.Unlock()

		return append([]*sandbox.Sandbox(nil), tracked...)
	}

	forceStop := func(items []*sandbox.Sandbox) error {
		firstForceStopOnce.Do(func() { close(firstForceStop) })

		mu.Lock()
		defer mu.Unlock()

		stopped += len(items)
		if len(items) > 0 {
			tracked = nil
		}

		return nil
	}

	waitSandboxStarts := func(context.Context) error {
		<-releaseWait

		return context.Canceled
	}

	done := make(chan []error, 1)
	go func() {
		done <- forceStopSandboxesUntilStartsFinish(t.Context(), lifecycleItems, forceStop, waitSandboxStarts)
	}()

	select {
	case <-firstForceStop:
	case <-time.After(time.Second):
		t.Fatal("force stop did not take initial lifecycle snapshot")
	}

	mu.Lock()
	tracked = []*sandbox.Sandbox{lateSandbox}
	mu.Unlock()
	close(releaseWait)

	select {
	case errs := <-done:
		require.ErrorIs(t, errors.Join(errs...), context.Canceled)
		require.Equal(t, 1, stopped)
	case <-time.After(time.Second):
		t.Fatal("force stop did not finish after start wait error")
	}
}

func TestDrainSandboxesDrainsFactoryAfterServerStartsFinish(t *testing.T) {
	t.Parallel()

	s := drainOrderTestServer()
	s.sandboxStartMu.RLock()
	locked := true
	defer func() {
		if locked {
			s.sandboxStartMu.RUnlock()
		}
	}()

	done := make(chan error, 1)
	go func() {
		done <- s.DrainSandboxes(t.Context())
	}()

	select {
	case <-s.done:
	case <-time.After(time.Second):
		t.Fatal("DrainSandboxes did not start draining")
	}

	time.Sleep(2 * sandboxStartWaitPollInterval)
	err := resumeSandboxStartErr(t.Context(), s.sandboxFactory)
	require.ErrorIs(t, err, errFactoryStartEntered)
	require.NotErrorIs(t, err, sandbox.ErrFactoryDraining)

	s.sandboxStartMu.RUnlock()
	locked = false

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("DrainSandboxes did not return after server starts finished")
	}

	err = resumeSandboxStartErr(t.Context(), s.sandboxFactory)
	require.ErrorIs(t, err, sandbox.ErrFactoryDraining)
}

func TestWaitForceStopSandboxesReturnsContextError(t *testing.T) {
	t.Parallel()

	var wg sync.WaitGroup
	wg.Add(1)
	defer wg.Done()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	require.ErrorIs(t, waitForceStopSandboxes(ctx, &wg), context.Canceled)
}

func forceStopTestServer() *Server {
	return &Server{
		done: make(chan struct{}),
		sandboxFactory: &sandbox.Factory{
			Sandboxes: sandbox.NewSandboxesMap(),
		},
	}
}

func drainOrderTestServer() *Server {
	return &Server{
		done:           make(chan struct{}),
		sandboxFactory: sandbox.NewFactory(cfg.BuilderConfig{}, nil, nil, nil, nil, nil, nil, sandbox.NewSandboxesMap()),
	}
}

func resumeSandboxStartErr(ctx context.Context, factory *sandbox.Factory) (err error) {
	defer func() {
		if recover() != nil {
			err = errFactoryStartEntered
		}
	}()

	_, err = factory.ResumeSandbox(ctx, nil, nil, sandbox.RuntimeMetadata{}, time.Time{}, time.Time{}, nil)

	return err
}
