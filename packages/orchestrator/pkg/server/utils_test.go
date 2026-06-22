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

	s := &Server{}
	release, err := s.drainGate.Enter()
	require.NoError(t, err)
	defer release()

	waitCtx, cancel := context.WithCancel(t.Context())
	waitErr := make(chan error, 1)
	go func() {
		waitErr <- s.waitSandboxStarts(waitCtx)
	}()

	cancel()

	select {
	case err := <-waitErr:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("waitSandboxStarts did not return after cancellation")
	}

	s.drainGate.StartDraining()

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

func TestWaitForAcquireAllowsAdmittedStartAfterDraining(t *testing.T) {
	t.Parallel()

	startingSandboxes, err := sharedutils.NewAdjustableSemaphore(1)
	require.NoError(t, err)

	s := &Server{startingSandboxes: startingSandboxes}
	release, err := s.enterSandboxStart(t.Context(), "test")
	require.NoError(t, err)
	defer release()

	s.drainGate.StartDraining()

	require.NoError(t, s.waitForAcquire(t.Context()))
	s.startingSandboxes.Release(1)
}

func TestForceStopSandboxesWaitsForInFlightStarts(t *testing.T) {
	t.Parallel()

	s := forceStopTestServer()
	release, err := s.drainGate.Enter()
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
	release, err := s.drainGate.Enter()
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

func TestDrainSandboxesDrainsFactoryAfterServerStartsFinish(t *testing.T) {
	t.Parallel()

	s := drainOrderTestServer()
	release, err := s.drainGate.Enter()
	require.NoError(t, err)

	done := make(chan error, 1)
	go func() {
		done <- s.DrainSandboxes(t.Context())
	}()

	select {
	case <-s.drainGate.Done():
	case <-time.After(time.Second):
		t.Fatal("DrainSandboxes did not start draining")
	}

	require.False(t, s.sandboxFactory.Draining())

	release()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("DrainSandboxes did not return after server starts finished")
	}

	require.True(t, s.sandboxFactory.Draining())
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
