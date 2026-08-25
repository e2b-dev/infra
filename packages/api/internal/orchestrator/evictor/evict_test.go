package evictor

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/launchdarkly/go-sdk-common/v3/ldlog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/metric/noop"

	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func TestEvictSandbox_ReasonByAction(t *testing.T) {
	t.Parallel()

	// Filesystem-only cases carry an FC release that qualifies for the
	// feature (e2b >= 0.2.0); runOn overrides it to pin the degrade.
	// Offline flags client: resolution falls back to the built-in
	// FirecrackerVersionMap, so each declared version resolves within its
	// own line — exactly the degrade semantics under test.
	flags, err := featureflags.NewClientWithLogLevel(ldlog.Error)
	require.NoError(t, err)

	counter, err := telemetry.GetCounter(noop.NewMeterProvider().Meter("github.com/e2b-dev/infra/packages/api/internal/orchestrator/evictor"), telemetry.ApiEvictorFsOnlyAutoPause)
	require.NoError(t, err)

	runOn := func(autoPause, autoPauseFilesystemOnly bool, fcVersion string) sandbox.RemoveOpts {
		var got sandbox.RemoveOpts
		called := false
		e := &Evictor{
			featureFlags:           flags,
			fsOnlyAutoPauseCounter: counter,
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

		e.evictSandbox(t.Context(), sandbox.Sandbox{
			SandboxID:               "sbx",
			TeamID:                  uuid.New(),
			AutoPause:               autoPause,
			AutoPauseFilesystemOnly: autoPauseFilesystemOnly,
			FirecrackerVersion:      fcVersion,
			EndTime:                 time.Now(),
		})

		require.True(t, called)

		return got
	}
	run := func(autoPause, autoPauseFilesystemOnly bool) sandbox.RemoveOpts {
		return runOn(autoPause, autoPauseFilesystemOnly, "v1.14-0.2.0")
	}

	t.Run("kill carries timeout reason", func(t *testing.T) {
		t.Parallel()

		got := run(false, false)

		assert.Equal(t, sandbox.StateActionKill, got.Action)
		assert.True(t, got.Eviction)
		assert.Equal(t, sandbox.KillReasonTimeout, got.Reason)
	})

	t.Run("kill ignores the auto-pause snapshot kind", func(t *testing.T) {
		t.Parallel()

		// AutoPauseFilesystemOnly is meaningless without AutoPause; a kill must
		// never carry it.
		got := run(false, true)

		assert.Equal(t, sandbox.StateActionKill, got.Action)
		assert.False(t, got.FilesystemOnly)
	})

	t.Run("auto-pause carries no kill reason", func(t *testing.T) {
		t.Parallel()

		got := run(true, false)

		assert.Equal(t, sandbox.StateActionPause, got.Action)
		assert.Empty(t, got.Reason)
	})

	t.Run("memory auto-pause is not filesystem-only", func(t *testing.T) {
		t.Parallel()

		got := run(true, false)

		assert.Equal(t, sandbox.StateActionPause, got.Action)
		assert.False(t, got.FilesystemOnly)
	})

	t.Run("filesystem-only auto-pause requests a filesystem-only snapshot", func(t *testing.T) {
		t.Parallel()

		got := run(true, true)

		assert.Equal(t, sandbox.StateActionPause, got.Action)
		assert.True(t, got.FilesystemOnly)
	})

	// The degrade: an fs-only policy on an FC whose release predates
	// filesystem-only snapshots (or whose version cannot be parsed) must
	// still PAUSE — with memory — never refuse or kill; a refusal here would
	// retry the eviction forever and any later refusal strands the VM for
	// the orphan reconciler.
	t.Run("filesystem-only auto-pause degrades to memory on a pre-0.2.0 release", func(t *testing.T) {
		t.Parallel()

		got := runOn(true, true, "v1.14.1_431f1fc")

		assert.Equal(t, sandbox.StateActionPause, got.Action)
		assert.False(t, got.FilesystemOnly)
	})

	t.Run("filesystem-only auto-pause degrades to memory on an unparsable version", func(t *testing.T) {
		t.Parallel()

		got := runOn(true, true, "")

		assert.Equal(t, sandbox.StateActionPause, got.Action)
		assert.False(t, got.FilesystemOnly)
	})
}
