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

	runOn := func(state sandbox.State, autoPause, autoPauseFilesystemOnly bool, fcVersion string) sandbox.RemoveOpts {
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
			ExecutionID:             "exec-1",
			State:                   state,
			AutoPause:               autoPause,
			AutoPauseFilesystemOnly: autoPauseFilesystemOnly,
			FirecrackerVersion:      fcVersion,
			EndTime:                 time.Now(),
		})

		require.True(t, called)

		return got
	}
	run := func(autoPause, autoPauseFilesystemOnly bool) sandbox.RemoveOpts {
		return runOn(sandbox.StateRunning, autoPause, autoPauseFilesystemOnly, "v1.14-0.2.0")
	}

	t.Run("kill carries timeout reason", func(t *testing.T) {
		t.Parallel()

		got := run(false, false)

		assert.Equal(t, sandbox.StateActionKill, got.Action)
		assert.True(t, got.Eviction)
		assert.Equal(t, sandbox.KillReasonTimeout, got.Reason)
	})

	t.Run("removal is pinned to the scanned execution", func(t *testing.T) {
		t.Parallel()

		assert.Equal(t, "exec-1", run(false, false).ExpectExecutionID)
		assert.Equal(t, "exec-1", run(true, false).ExpectExecutionID)
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

	// No version gate: the fs-only policy is honored on every FC version,
	// legacy and unparsable included — producing a filesystem-only snapshot
	// needs no version-gated capability.
	t.Run("filesystem-only auto-pause is honored on a legacy release", func(t *testing.T) {
		t.Parallel()

		got := runOn(sandbox.StateRunning, true, true, "v1.14.1_431f1fc")

		assert.Equal(t, sandbox.StateActionPause, got.Action)
		assert.True(t, got.FilesystemOnly)
	})

	t.Run("filesystem-only auto-pause is honored on an unparsable version", func(t *testing.T) {
		t.Parallel()

		got := runOn(sandbox.StateRunning, true, true, "")

		assert.Equal(t, sandbox.StateActionPause, got.Action)
		assert.True(t, got.FilesystemOnly)
	})

	t.Run("auto-pause leftover in killing is killed not paused", func(t *testing.T) {
		t.Parallel()

		got := runOn(sandbox.StateKilling, true, true, "v1.14-0.2.0")

		assert.Equal(t, sandbox.StateActionKill, got.Action)
		assert.True(t, got.Eviction)
		assert.Equal(t, sandbox.KillReasonTimeout, got.Reason)
		assert.False(t, got.FilesystemOnly)
	})

	t.Run("auto-pause leftover that can still pause is paused", func(t *testing.T) {
		t.Parallel()

		for _, state := range []sandbox.State{sandbox.StatePausing, sandbox.StateSnapshotting} {
			t.Run(string(state), func(t *testing.T) {
				t.Parallel()

				got := runOn(state, true, true, "v1.14-0.2.0")

				assert.Equal(t, sandbox.StateActionPause, got.Action)
				assert.True(t, got.FilesystemOnly)
			})
		}
	})
}

func TestCanTake(t *testing.T) {
	t.Parallel()

	assert.True(t, canTake(sandbox.StateRunning, sandbox.StateActionPause))
	assert.True(t, canTake(sandbox.StatePausing, sandbox.StateActionPause))
	assert.True(t, canTake(sandbox.StateSnapshotting, sandbox.StateActionPause))
	assert.False(t, canTake(sandbox.StateKilling, sandbox.StateActionPause))
	assert.True(t, canTake(sandbox.StateKilling, sandbox.StateActionKill))
}

func TestIsStaleDecision(t *testing.T) {
	t.Parallel()

	t.Run("state moved since the scan", func(t *testing.T) {
		t.Parallel()

		err := &sandbox.InvalidStateTransitionError{
			CurrentState: sandbox.StateKilling,
			TargetState:  sandbox.StatePausing,
		}
		assert.True(t, isStaleDecision(err, sandbox.StateRunning))
		assert.True(t, isKnownEvictionError(err, sandbox.StateRunning))
	})

	t.Run("refusal from the scanned state stays a failure", func(t *testing.T) {
		t.Parallel()

		// An unknown state is refused from the same state it was scanned in.
		// Nothing moved; the record is broken.
		err := &sandbox.InvalidStateTransitionError{
			CurrentState: "",
			TargetState:  sandbox.StateKilling,
		}
		assert.False(t, isStaleDecision(err, ""))
		assert.False(t, isKnownEvictionError(err, ""))
	})

	t.Run("other errors are not stale decisions", func(t *testing.T) {
		t.Parallel()

		assert.False(t, isStaleDecision(sandbox.ErrNotFound, sandbox.StateRunning))
	})
}

func TestIsGone(t *testing.T) {
	t.Parallel()

	assert.True(t, isGone(sandbox.ErrNotFound))
	assert.True(t, isGone(sandbox.ErrExecutionMismatch))
	assert.True(t, isKnownEvictionError(sandbox.ErrExecutionMismatch, sandbox.StateRunning))
	assert.False(t, isGone(sandbox.ErrEvictionNotNeeded))
}
