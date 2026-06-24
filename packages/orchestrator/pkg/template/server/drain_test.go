//go:build linux

package server

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	containerregistry "github.com/google/go-containerregistry/pkg/v1"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/metric/noop"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/template/build/builderrors"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/template/build/buildlogger"
	templatecache "github.com/e2b-dev/infra/packages/orchestrator/pkg/template/cache"
	templatemanager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

// fakeArtifactsRegistry is a no-op ArtifactsRegistry for delete tests.
type fakeArtifactsRegistry struct{}

func (*fakeArtifactsRegistry) GetTag(context.Context, string, string) (string, error) {
	return "", nil
}

func (*fakeArtifactsRegistry) GetImage(context.Context, string, string, containerregistry.Platform) (containerregistry.Image, error) {
	return nil, nil
}

func (*fakeArtifactsRegistry) Delete(context.Context, string, string) error {
	return nil
}

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

func TestWaitTemplateOperationStartsCanceledDoesNotBlockDrainingRejection(t *testing.T) {
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
		waitErr <- s.waitTemplateOperationStarts(waitCtx)
	}()

	cancel()

	select {
	case err := <-waitErr:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("waitTemplateOperationStarts did not return after cancellation")
	}

	s.StartDraining(t.Context())

	enterErr := make(chan error, 1)
	go func() {
		release, err := s.enterTemplateOperationStart(t.Context(), "test")
		if err == nil {
			release()
		}

		enterErr <- err
	}()

	select {
	case err := <-enterErr:
		require.Equal(t, codes.Unavailable, status.Code(err))
	case <-time.After(time.Second):
		t.Fatal("enterTemplateOperationStart blocked instead of rejecting while draining")
	}
}

func TestTemplateBuildDeleteAllowedAndCancelsBuildDuringDrain(t *testing.T) {
	t.Parallel()

	buildCache := templatecache.NewBuildCache(t.Context(), noop.NewMeterProvider())
	templateStorage := storage.NewMockStorageProvider(t)
	templateStorage.EXPECT().DeleteObjectsWithPrefix(mock.Anything, "build-id").Return(nil)

	s := &ServerStore{
		logger:            logger.NewNopLogger(),
		wg:                &sync.WaitGroup{},
		buildCache:        buildCache,
		templateStorage:   templateStorage,
		artifactsregistry: &fakeArtifactsRegistry{},
	}

	buildInfo, err := buildCache.Create("team-id", "build-id", buildlogger.NewLogEntryLogger())
	require.NoError(t, err)

	// Drain must not stop cancels/kills of in-flight builds.
	s.StartDraining(t.Context())

	got, err := s.TemplateBuildDelete(t.Context(), &templatemanager.TemplateBuildDeleteRequest{
		TemplateID: "template-id",
		BuildID:    "build-id",
	})
	require.NoError(t, err)
	require.NotNil(t, got)

	select {
	case <-buildInfo.Result.Done:
		result, err := buildInfo.Result.Result()
		require.NoError(t, err)
		require.Equal(t, templatemanager.TemplateBuildState_Failed, result.Status)
		require.Equal(t, builderrors.ErrCanceled.Error(), result.Reason.GetMessage())
	default:
		t.Fatal("delete during drain did not cancel the running build")
	}
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
