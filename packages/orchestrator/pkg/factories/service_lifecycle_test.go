package factories

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

func TestStartManagedServiceReturnsServiceError(t *testing.T) {
	t.Parallel()

	expectedErr := errors.New("serve failed")
	serviceExited := make(chan serviceExit, 1)
	var g errgroup.Group

	startManagedService(context.Background(), &g, serviceExited, "grpc server", logger.NewNopLogger(), func() error {
		return expectedErr
	})

	exit := <-serviceExited
	assert.Equal(t, "grpc server", exit.name)
	require.ErrorIs(t, exit.err, expectedErr)

	err := g.Wait()
	require.ErrorIs(t, err, expectedErr)
	assert.ErrorContains(t, err, "service grpc server failed")
}

func TestStartManagedServiceBuffersEarlyServiceExit(t *testing.T) {
	t.Parallel()

	serviceExited := make(chan serviceExit, 1)
	var g errgroup.Group

	startManagedService(context.Background(), &g, serviceExited, "host metrics poller", logger.NewNopLogger(), func() error {
		return nil
	})

	require.NoError(t, g.Wait())

	select {
	case exit := <-serviceExited:
		assert.Equal(t, "host metrics poller", exit.name)
		require.NoError(t, exit.err)
	default:
		t.Fatal("service exit was not buffered")
	}
}
