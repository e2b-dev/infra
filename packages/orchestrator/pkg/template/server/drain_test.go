//go:build linux

package server

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	templatemanager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

func TestWaitBuildStartsCanceledDoesNotBlockDrainingRejection(t *testing.T) {
	t.Parallel()

	s := &ServerStore{
		logger:    logger.NewNopLogger(),
		drainDone: make(chan struct{}),
	}

	s.buildStartMu.RLock()
	defer s.buildStartMu.RUnlock()

	waitCtx, cancel := context.WithCancel(t.Context())
	waitErr := make(chan error, 1)
	go func() {
		waitErr <- s.waitBuildStarts(waitCtx)
	}()

	time.Sleep(2 * buildStartWaitPollInterval)
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
		logger:    logger.NewNopLogger(),
		drainDone: make(chan struct{}),
		wg:        &sync.WaitGroup{},
	}
	s.StartDraining(t.Context())

	got, err := s.TemplateBuildDelete(t.Context(), &templatemanager.TemplateBuildDeleteRequest{
		TemplateID: "template-id",
		BuildID:    "build-id",
	})
	require.Equal(t, codes.Unavailable, status.Code(err))
	require.Nil(t, got)
}
