//go:build linux

package sandbox

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

func newTestSandboxForExit(endAt time.Time) *Sandbox {
	sbx := &Sandbox{
		Metadata: &Metadata{},
		exit:     utils.NewErrorOnce(),
	}
	sbx.SetEndAt(endAt)

	return sbx
}

// TestWaitForExit_AlreadyExpired verifies that WaitForExit returns an error
// immediately when endAt is already in the past.
func TestWaitForExit_AlreadyExpired(t *testing.T) {
	t.Parallel()

	sbx := newTestSandboxForExit(time.Now().Add(-1 * time.Second))

	err := sbx.WaitForExit(context.Background())

	require.Error(t, err)
}

// TestWaitForExit_ExitBeforeTTL verifies that WaitForExit returns nil when
// the sandbox exits cleanly before its TTL expires.
func TestWaitForExit_ExitBeforeTTL(t *testing.T) {
	t.Parallel()

	sbx := newTestSandboxForExit(time.Now().Add(2 * time.Second))

	go func() {
		time.Sleep(50 * time.Millisecond)
		sbx.exit.SetSuccess()
	}()

	err := sbx.WaitForExit(context.Background())

	require.NoError(t, err)
}

// TestWaitForExit_ExitWithError verifies that WaitForExit returns a wrapped
// error when the Firecracker process exits with an error before the TTL.
func TestWaitForExit_ExitWithError(t *testing.T) {
	t.Parallel()

	sbx := newTestSandboxForExit(time.Now().Add(2 * time.Second))

	fcErr := errors.New("firecracker process killed")
	go func() {
		time.Sleep(50 * time.Millisecond)
		sbx.exit.SetError(fcErr)
	}()

	err := sbx.WaitForExit(context.Background())

	require.Error(t, err)
	assert.ErrorContains(t, err, "fc process exited prematurely")
	assert.ErrorContains(t, err, fcErr.Error())
}

// TestWaitForExit_KeepAliveExtendsTTL verifies that a KeepAlive call that
// extends endAt via SetEndAt resets the node-side timer. Without this, the
// sandbox would be killed at the original TTL even though it was extended.
func TestWaitForExit_KeepAliveExtendsTTL(t *testing.T) {
	t.Parallel()

	// Short initial TTL — would fire after 80 ms without the fix.
	sbx := newTestSandboxForExit(time.Now().Add(80 * time.Millisecond))

	done := make(chan error, 1)
	go func() {
		done <- sbx.WaitForExit(context.Background())
	}()

	// Simulate KeepAlive: extend endAt before the original TTL fires.
	time.Sleep(40 * time.Millisecond)
	sbx.SetEndAt(time.Now().Add(2 * time.Second))

	// Wait past the original TTL; WaitForExit must NOT have returned yet.
	time.Sleep(100 * time.Millisecond)
	select {
	case err := <-done:
		t.Fatalf("WaitForExit returned early after KeepAlive extension: %v", err)
	default:
		// Good — still waiting.
	}

	// Now signal a clean exit; WaitForExit should return nil.
	sbx.exit.SetSuccess()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("WaitForExit did not return after exit signal")
	}
}

// TestWaitForExit_ContextCancelled verifies that WaitForExit returns nil when
// the context is cancelled before either TTL expiry or process exit.
func TestWaitForExit_ContextCancelled(t *testing.T) {
	t.Parallel()

	sbx := newTestSandboxForExit(time.Now().Add(10 * time.Second))

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- sbx.WaitForExit(ctx)
	}()

	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("WaitForExit did not return after context cancellation")
	}
}
