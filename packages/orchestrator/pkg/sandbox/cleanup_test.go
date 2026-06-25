//go:build linux

package sandbox

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type cleanupTestKey struct{}

func TestCleanupRunIgnoresParentCancelButPreservesDeadline(t *testing.T) {
	t.Parallel()

	cleanup := NewCleanup()
	deadline := time.Now().Add(time.Hour)
	called := false
	cleanup.Add(t.Context(), func(ctx context.Context) error {
		called = true

		cleanupDeadline, ok := ctx.Deadline()
		require.True(t, ok)
		require.True(t, cleanupDeadline.Equal(deadline))
		require.NoError(t, ctx.Err())

		return nil
	})

	ctx, cancel := context.WithDeadline(t.Context(), deadline)
	cancel()

	require.NoError(t, cleanup.Run(ctx))
	require.True(t, called)
}

func TestCleanupRunRollbackIgnoresParentCancelAndDeadline(t *testing.T) {
	t.Parallel()

	cleanup := NewCleanup()
	called := false
	cleanup.Add(t.Context(), func(ctx context.Context) error {
		called = true

		_, ok := ctx.Deadline()
		require.False(t, ok)
		require.NoError(t, ctx.Err())
		require.Equal(t, "value", ctx.Value(cleanupTestKey{}))

		return nil
	})

	parent := context.WithValue(t.Context(), cleanupTestKey{}, "value")
	ctx, cancel := context.WithDeadline(parent, time.Now().Add(-time.Second))
	defer cancel()

	require.ErrorIs(t, ctx.Err(), context.DeadlineExceeded)
	require.NoError(t, cleanup.RunRollback(ctx))
	require.True(t, called)
}

func TestCleanupRunRollbackUsesDetachedContextForLateAdd(t *testing.T) {
	t.Parallel()

	cleanup := NewCleanup()
	parent := context.WithValue(t.Context(), cleanupTestKey{}, "value")
	ctx, cancel := context.WithDeadline(parent, time.Now().Add(-time.Second))
	defer cancel()

	require.NoError(t, cleanup.RunRollback(ctx))

	called := false
	cleanup.Add(ctx, func(ctx context.Context) error {
		called = true

		_, ok := ctx.Deadline()
		require.False(t, ok)
		require.NoError(t, ctx.Err())
		require.Equal(t, "value", ctx.Value(cleanupTestKey{}))

		return nil
	})

	require.True(t, called)
}
