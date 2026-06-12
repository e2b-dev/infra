//go:build linux

package server

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/metric/noop"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/template/build/builderrors"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/template/build/buildlogger"
	templatecache "github.com/e2b-dev/infra/packages/orchestrator/pkg/template/cache"
	templatemanager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

type fakeCloser struct {
	closed bool
	err    error
}

func (f *fakeCloser) Close() error {
	f.closed = true

	return f.err
}

func TestCloseRunsClosersWithCanceledContext(t *testing.T) {
	t.Parallel()

	first := &fakeCloser{}
	second := &fakeCloser{}
	s := &ServerStore{
		logger:  logger.NewNopLogger(),
		closers: []closeable{first, second},
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	require.NoError(t, s.Close(ctx))
	require.True(t, first.closed)
	require.True(t, second.closed)
}

func TestCloseJoinsCloserErrors(t *testing.T) {
	t.Parallel()

	failing := &fakeCloser{err: errors.New("close failed")}
	ok := &fakeCloser{}
	s := &ServerStore{
		logger:  logger.NewNopLogger(),
		closers: []closeable{failing, ok},
	}

	err := s.Close(t.Context())
	require.ErrorContains(t, err, "close failed")
	require.True(t, ok.closed)
}

func TestWaitBuildStartsCanceledDoesNotBlockDrainingRejection(t *testing.T) {
	t.Parallel()

	s := &ServerStore{
		logger: logger.NewNopLogger(),
	}

	release, err := s.drainGate.Enter()
	require.NoError(t, err)
	defer release()

	waitCtx, cancel := context.WithCancel(t.Context())
	waitErr := make(chan error, 1)
	go func() {
		waitErr <- s.waitBuildStarts(waitCtx)
	}()

	cancel()

	select {
	case err := <-waitErr:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("waitBuildStarts did not return after cancellation")
	}

	s.StartDraining(t.Context())

	enterErr := make(chan error, 1)
	go func() {
		release, err := s.enterBuildStart(t.Context(), "test")
		if err == nil {
			release()
		}

		enterErr <- err
	}()

	select {
	case err := <-enterErr:
		require.Equal(t, codes.Unavailable, status.Code(err))
	case <-time.After(time.Second):
		t.Fatal("enterBuildStart blocked instead of rejecting while draining")
	}
}

func TestTemplateBuildDeleteRejectsAfterDrainStarts(t *testing.T) {
	t.Parallel()

	s := &ServerStore{
		logger: logger.NewNopLogger(),
		wg:     &sync.WaitGroup{},
	}
	s.StartDraining(t.Context())

	got, err := s.TemplateBuildDelete(t.Context(), &templatemanager.TemplateBuildDeleteRequest{
		TemplateID: "template-id",
		BuildID:    "build-id",
	})
	require.Equal(t, codes.Unavailable, status.Code(err))
	require.Nil(t, got)
}

func TestStartDrainingDoesNotCancelRunningBuild(t *testing.T) {
	t.Parallel()

	buildCache := templatecache.NewBuildCache(t.Context(), noop.NewMeterProvider())
	s := &ServerStore{
		logger:     logger.NewNopLogger(),
		buildCache: buildCache,
	}
	buildInfo, err := buildCache.Create("team-id", "build-id", buildlogger.NewLogEntryLogger())
	require.NoError(t, err)

	s.StartDraining(t.Context())

	select {
	case <-buildInfo.Result.Done:
		t.Fatal("normal drain canceled running build")
	default:
	}
}

func TestWaitCanceledContextCancelsBuildsAndReturns(t *testing.T) {
	t.Parallel()

	buildCache := templatecache.NewBuildCache(t.Context(), noop.NewMeterProvider())
	s := &ServerStore{
		logger:     logger.NewNopLogger(),
		wg:         &sync.WaitGroup{},
		buildCache: buildCache,
	}
	s.wg.Add(1)
	defer s.wg.Done()

	buildInfo, err := buildCache.Create("team-id", "build-id", buildlogger.NewLogEntryLogger())
	require.NoError(t, err)

	waitCtx, cancel := context.WithCancel(t.Context())
	cancel()

	waitErr := make(chan error, 1)
	go func() {
		waitErr <- s.ForceStop(waitCtx)
	}()

	select {
	case <-buildInfo.Result.Done:
		result, err := buildInfo.Result.Result()
		require.NoError(t, err)
		require.Equal(t, templatemanager.TemplateBuildState_Failed, result.Status)
		require.Equal(t, builderrors.ErrCanceled.Error(), result.Reason.GetMessage())
	case <-time.After(time.Second):
		t.Fatal("ForceStop did not cancel running build after build start gate drained")
	}

	select {
	case err := <-waitErr:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("ForceStop blocked on active build after forced cancellation")
	}
}

func TestForceStopCancelsBuildCreatedDuringBuildStartDrain(t *testing.T) {
	t.Parallel()

	buildCache := templatecache.NewBuildCache(t.Context(), noop.NewMeterProvider())
	s := &ServerStore{
		logger:     logger.NewNopLogger(),
		wg:         &sync.WaitGroup{},
		buildCache: buildCache,
	}
	sentinelBuild, err := buildCache.Create("team-id", "sentinel-build", buildlogger.NewLogEntryLogger())
	require.NoError(t, err)

	release, err := s.drainGate.Enter()
	require.NoError(t, err)
	defer release()

	waitCtx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()

	waitErr := make(chan error, 1)
	go func() {
		waitErr <- s.ForceStop(waitCtx)
	}()

	select {
	case <-s.drainGate.Done():
	case <-time.After(time.Second):
		t.Fatal("ForceStop did not start draining")
	}

	select {
	case <-sentinelBuild.Result.Done:
	case <-time.After(time.Second):
		t.Fatal("ForceStop did not run the first forced cancellation pass")
	}

	select {
	case err := <-waitErr:
		t.Fatalf("ForceStop returned before in-flight build start finished: %v", err)
	default:
	}

	buildInfo, err := buildCache.Create("team-id", "build-id", buildlogger.NewLogEntryLogger())
	require.NoError(t, err)
	s.wg.Add(1)
	select {
	case <-buildInfo.Result.Done:
		t.Fatal("late build was canceled before build start drain completed")
	default:
	}

	release()

	select {
	case <-buildInfo.Result.Done:
		result, err := buildInfo.Result.Result()
		require.NoError(t, err)
		require.Equal(t, templatemanager.TemplateBuildState_Failed, result.Status)
		require.Equal(t, builderrors.ErrCanceled.Error(), result.Reason.GetMessage())
	case <-time.After(time.Second):
		t.Fatal("ForceStop did not cancel build created during build start drain")
	}

	s.wg.Done()

	select {
	case err := <-waitErr:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("ForceStop did not return after build finished")
	}
}
