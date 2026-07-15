//go:build linux

package sandbox

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/cfg"
)

type startHookFunc func(context.Context, *Sandbox, StartReason) error

func (f startHookFunc) BeforeStart(ctx context.Context, sbx *Sandbox, reason StartReason) error {
	return f(ctx, sbx, reason)
}

func testStartHookSandbox() *Sandbox {
	return &Sandbox{
		Metadata:    &Metadata{Runtime: RuntimeMetadata{SandboxID: "sandbox-id"}},
		LifecycleID: "lifecycle-id",
	}
}

func TestFactoryRunStartHookSuccess(t *testing.T) {
	t.Parallel()

	sbx := testStartHookSandbox()
	var gotSandbox *Sandbox
	var gotReason StartReason
	var hasDeadline bool
	factory := &Factory{startHook: startHookFunc(func(ctx context.Context, got *Sandbox, reason StartReason) error {
		gotSandbox = got
		gotReason = reason
		_, hasDeadline = ctx.Deadline()

		return nil
	})}

	factory.runStartHook(t.Context(), sbx, StartReasonResume)

	require.Same(t, sbx, gotSandbox)
	require.Equal(t, StartReasonResume, gotReason)
	require.True(t, hasDeadline)
}

func TestFactoryRunStartHookSwallowsError(t *testing.T) {
	t.Parallel()

	factory := &Factory{startHook: startHookFunc(func(context.Context, *Sandbox, StartReason) error {
		return errors.New("hook failed")
	})}

	require.NotPanics(t, func() {
		factory.runStartHook(t.Context(), testStartHookSandbox(), StartReasonResume)
	})
}

func TestFactoryRunStartHookRecoversPanic(t *testing.T) {
	t.Parallel()

	factory := &Factory{startHook: startHookFunc(func(context.Context, *Sandbox, StartReason) error {
		panic("hook panicked")
	})}

	require.NotPanics(t, func() {
		factory.runStartHook(t.Context(), testStartHookSandbox(), StartReasonResume)
	})
}

func TestFactoryRunStartHookBoundsContextIgnoringHook(t *testing.T) {
	t.Parallel()

	factory := &Factory{startHook: startHookFunc(func(context.Context, *Sandbox, StartReason) error {
		select {}
	})}

	started := time.Now()
	factory.runStartHook(t.Context(), testStartHookSandbox(), StartReasonResume)
	require.Less(t, time.Since(started), startHookTimeout+500*time.Millisecond)
}

func TestNewFactoryDefaultsNilStartHook(t *testing.T) {
	t.Parallel()

	factory := NewFactory(cfg.BuilderConfig{}, nil, nil, nil, nil, nil, nil, nil, nil)

	require.IsType(t, NoopStartHook{}, factory.startHook)
}

func TestFactoryRunStartHookSuppressesContextErrors(t *testing.T) {
	t.Parallel()

	for _, err := range []error{context.Canceled, context.DeadlineExceeded} {
		factory := &Factory{startHook: startHookFunc(func(context.Context, *Sandbox, StartReason) error {
			return err
		})}
		require.NotPanics(t, func() {
			factory.runStartHook(t.Context(), testStartHookSandbox(), StartReasonResume)
		})
	}
}

func TestFactoryRunStartHookReturnsPromptlyOnParentCancellation(t *testing.T) {
	t.Parallel()

	factory := &Factory{startHook: startHookFunc(func(context.Context, *Sandbox, StartReason) error {
		select {}
	})}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	started := time.Now()
	factory.runStartHook(ctx, testStartHookSandbox(), StartReasonResume)
	require.Less(t, time.Since(started), 500*time.Millisecond, "parent cancellation must not wait for startHookTimeout")
}

func TestStartReasonString(t *testing.T) {
	t.Parallel()

	cases := map[StartReason]string{
		StartReasonUnknown:         "unknown",
		StartReasonCreate:          "create",
		StartReasonResume:          "resume",
		StartReasonReboot:          "reboot",
		StartReasonThrowawayResume: "throwaway_resume",
		StartReason(255):           "unknown",
	}
	for reason, want := range cases {
		require.Equal(t, want, reason.String())
	}
}

// BenchmarkFactoryRunStartHook measures the cost this extension point adds to
// every CreateSandbox/ResumeSandbox call when no edition wires in a real
// StartHook (the common case for any build that doesn't configure one).
// Measured in the low single-digit microseconds (576 B/op, 7 allocs/op,
// dominated by the goroutine this spawns to enforce startHookTimeout) -
// negligible against a sandbox create/resume, which runs in the
// milliseconds-to-seconds range. Re-run locally for current numbers rather
// than trusting a hardcoded figure here, which will drift with hardware.
func BenchmarkFactoryRunStartHook(b *testing.B) {
	factory := &Factory{startHook: NoopStartHook{}}
	sbx := testStartHookSandbox()
	ctx := b.Context()

	b.ReportAllocs()
	for b.Loop() {
		factory.runStartHook(ctx, sbx, StartReasonResume)
	}
}
