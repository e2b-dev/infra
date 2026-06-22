package utils

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type contextTestKey struct{}

func TestWithoutCancelPreservingDeadlineIgnoresCancelButKeepsDeadline(t *testing.T) {
	t.Parallel()

	parentDeadline := time.Now().Add(time.Hour)
	parent, parentCancel := context.WithDeadline(t.Context(), parentDeadline)
	defer parentCancel()

	ctx, cancel := WithoutCancelPreservingDeadline(parent)
	defer cancel()

	deadline, ok := ctx.Deadline()
	require.True(t, ok)
	require.True(t, deadline.Equal(parentDeadline))

	parentCancel()
	select {
	case <-ctx.Done():
		require.Fail(t, "detached context was canceled by parent cancellation")
	default:
	}
}

func TestWithoutCancelPreservingDeadlineExpiresAtDeadline(t *testing.T) {
	t.Parallel()

	parent, parentCancel := context.WithDeadline(t.Context(), time.Now().Add(10*time.Millisecond))
	defer parentCancel()

	ctx, cancel := WithoutCancelPreservingDeadline(parent)
	defer cancel()

	select {
	case <-ctx.Done():
		require.ErrorIs(t, ctx.Err(), context.DeadlineExceeded)
	case <-time.After(time.Second):
		t.Fatal("detached context did not keep parent deadline")
	}
}

func TestWithoutCancelPreservingDeadlineKeepsValues(t *testing.T) {
	t.Parallel()

	parent := context.WithValue(t.Context(), contextTestKey{}, "value")
	ctx, cancel := WithoutCancelPreservingDeadline(parent)
	defer cancel()

	require.Equal(t, "value", ctx.Value(contextTestKey{}))
	_, ok := ctx.Deadline()
	require.False(t, ok)
}
