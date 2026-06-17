package evictor

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
)

func TestEvictSandbox_ReasonByAction(t *testing.T) {
	t.Parallel()

	run := func(autoPause bool) sandbox.RemoveOpts {
		var got sandbox.RemoveOpts
		called := false
		e := &Evictor{
			removeSandbox: func(
				_ context.Context,
				_ uuid.UUID,
				_ string,
				opts sandbox.RemoveOpts,
			) error {
				got = opts
				called = true

				return nil
			},
		}

		e.evictSandbox(context.Background(), sandbox.Sandbox{
			SandboxID: "sbx",
			TeamID:    uuid.Must(uuid.NewV7()),
			AutoPause: autoPause,
			EndTime:   time.Now(),
		})

		require.True(t, called)

		return got
	}

	t.Run("kill carries timeout reason", func(t *testing.T) {
		t.Parallel()

		got := run(false)

		assert.Equal(t, sandbox.StateActionKill, got.Action)
		assert.True(t, got.Eviction)
		assert.Equal(t, sandbox.KillReasonTimeout, got.Reason)
	})

	t.Run("auto-pause carries no kill reason", func(t *testing.T) {
		t.Parallel()

		got := run(true)

		assert.Equal(t, sandbox.StateActionPause, got.Action)
		assert.Empty(t, got.Reason)
	})
}
