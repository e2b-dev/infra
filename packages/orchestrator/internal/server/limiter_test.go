package server

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sharedstate"
	featureflags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
)

func TestAcquire(t *testing.T) {
	t.Run("successful acquire", func(t *testing.T) {
		client, err := featureflags.NewClient()
		require.NoError(t, err)

		mgr, err := sharedstate.New(time.Hour)
		require.NoError(t, err)

		l := NewLimiter(1, client, mgr)
		err = l.AcquireStarting(t.Context())
		require.NoError(t, err)
	})

	t.Run("failed acquire - too many sandboxes running", func(t *testing.T) {
		client, err := featureflags.NewClient()
		require.NoError(t, err)

		// get the limit so we can prep the shared state manager
		maxSandboxes, err := client.IntFlag(t.Context(), featureflags.MaxSandboxesPerNode)
		require.NoError(t, err)

		mgr, err := sharedstate.New(time.Hour)
		require.NoError(t, err)

		// prep the shared state
		var sbx *sandbox.Sandbox
		for range maxSandboxes {
			sbx = &sandbox.Sandbox{
				Metadata: &sandbox.Metadata{
					Runtime: sandbox.RuntimeMetadata{
						SandboxID: uuid.NewString(),
					},
				},
			}
			mgr.OnInsert(t.Context(), sbx)
		}
		require.NotNil(t, sbx)

		runningCount := mgr.TotalRunningCount()
		require.Equal(t, maxSandboxes, runningCount)

		l := NewLimiter(1, client, mgr)

		// try to acquire a starting slot
		err = l.AcquireStarting(t.Context())
		require.Error(t, err)
		var tmsrerr TooManySandboxesRunningError
		require.ErrorAs(t, err, &tmsrerr)

		// remove the last sbx
		mgr.OnRemove(t.Context(), sbx.Metadata.Runtime.SandboxID)

		// verify we can get the slot
		err = l.AcquireStarting(t.Context())
		require.NoError(t, err)
	})

	t.Run("failed acquire - too many sandboxes starting", func(t *testing.T) {
		client, err := featureflags.NewClient()
		require.NoError(t, err)

		mgr, err := sharedstate.New(time.Hour)
		require.NoError(t, err)

		l := NewLimiter(0, client, mgr)
		err = l.AcquireStarting(t.Context())
		require.Error(t, err)
		var tmsserr TooManySandboxesStartingError
		require.ErrorAs(t, err, &tmsserr)
	})
}
