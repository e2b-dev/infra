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
	sharedutils "github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

func TestWaitSandboxStartsCanceledDoesNotBlockDrainingRejection(t *testing.T) {
	t.Parallel()

	s := drainOrderTestServer()
	release, err := s.sandboxFactory.EnterSandboxStart(t.Context())
	require.NoError(t, err)
	defer release()

	waitCtx, cancel := context.WithCancel(t.Context())
	waitErr := make(chan error, 1)
	go func() {
		waitErr <- s.sandboxFactory.WaitSandboxStarts(waitCtx)
	}()

	cancel()

	select {
	case err := <-waitErr:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("WaitSandboxStarts did not return after cancellation")
	}

	s.sandboxFactory.StartDraining(t.Context())

	enterErr := make(chan error, 1)
	go func() {
		_, release, err := s.enterSandboxStart(t.Context(), "test")
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

// TestEnterSandboxStartReentrantWhileDraining covers the single-gate collapse:
// an operation that already holds the gate (Checkpoint) can run its nested
// factory start even after drain begins, while a fresh start is rejected.
func TestEnterSandboxStartReentrantWhileDraining(t *testing.T) {
	t.Parallel()

	s := drainOrderTestServer()
	ctx, release, err := s.enterSandboxStart(t.Context(), "checkpoint")
	require.NoError(t, err)
	defer release()

	s.sandboxFactory.StartDraining(t.Context())

	// The checkpoint's internal resume runs on the held context and must not be
	// rejected mid-drain.
	nestedRelease, err := s.sandboxFactory.EnterSandboxStart(ctx)
	require.NoError(t, err)
	nestedRelease()

	// A fresh, unmarked start is still rejected.
	_, err = s.sandboxFactory.EnterSandboxStart(t.Context())
	require.ErrorIs(t, err, sandbox.ErrFactoryDraining)
}

func TestWaitForAcquireAllowsAdmittedStartAfterDraining(t *testing.T) {
	t.Parallel()

	startingSandboxes, err := sharedutils.NewAdjustableSemaphore(1)
	require.NoError(t, err)

	s := drainOrderTestServer()
	s.startingSandboxes = startingSandboxes

	ctx, release, err := s.enterSandboxStart(t.Context(), "test")
	require.NoError(t, err)
	defer release()

	s.sandboxFactory.StartDraining(t.Context())

	require.NoError(t, s.waitForAcquire(ctx))
	s.startingSandboxes.Release(1)
}

func TestForceStopSandboxesWaitsForInFlightStarts(t *testing.T) {
	t.Parallel()

	s := forceStopTestServer()
	release, err := s.sandboxFactory.EnterSandboxStart(t.Context())
	require.NoError(t, err)

	done := make(chan error, 1)
	go func() {
		done <- s.ForceStopSandboxes(t.Context())
	}()

	select {
	case err := <-done:
		require.Failf(t, "ForceStopSandboxes returned before start left", "err: %v", err)
	case <-time.After(2 * sandboxStartWaitPollInterval):
	}

	release()

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
	release, err := s.sandboxFactory.EnterSandboxStart(t.Context())
	require.NoError(t, err)
	defer release()

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

func TestDrainSandboxesWaitsForInFlightStart(t *testing.T) {
	t.Parallel()

	s := drainOrderTestServer()
	release, err := s.sandboxFactory.EnterSandboxStart(t.Context())
	require.NoError(t, err)

	done := make(chan error, 1)
	go func() {
		done <- s.DrainSandboxes(t.Context())
	}()

	// Drain begins immediately, before the in-flight start finishes.
	select {
	case <-s.sandboxFactory.Done():
	case <-time.After(time.Second):
		t.Fatal("DrainSandboxes did not start draining")
	}
	require.True(t, s.sandboxFactory.Draining())

	// But it must not complete while a start is still in flight.
	select {
	case err := <-done:
		require.Failf(t, "DrainSandboxes returned before in-flight start finished", "err: %v", err)
	case <-time.After(2 * sandboxStartWaitPollInterval):
	}

	release()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("DrainSandboxes did not return after in-flight start finished")
	}
}

func TestCollectForceStopSandboxCloseErrorsDrainsBufferedErrorsOnContextError(t *testing.T) {
	t.Parallel()

	closeErr := errors.New("close failed")
	var wg sync.WaitGroup
	wg.Add(1)

	errCh := make(chan error, 1)
	sent := make(chan struct{})
	release := make(chan struct{})
	go func() {
		errCh <- closeErr
		close(sent)
		<-release
		wg.Done()
	}()
	defer close(release)

	select {
	case <-sent:
	case <-time.After(time.Second):
		t.Fatal("close goroutine did not send error")
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	closeErrs, err := collectForceStopSandboxCloseErrors(ctx, &wg, errCh)
	require.ErrorIs(t, err, context.Canceled)
	require.ErrorIs(t, errors.Join(closeErrs...), closeErr)
}

func forceStopTestServer() *Server {
	return &Server{
		sandboxFactory: &sandbox.Factory{
			Sandboxes: sandbox.NewSandboxesMap(),
		},
	}
}

func drainOrderTestServer() *Server {
	return &Server{
		sandboxFactory: sandbox.NewFactory(cfg.BuilderConfig{}, nil, nil, nil, nil, nil, nil, sandbox.NewSandboxesMap()),
	}
}
