//go:build linux

package sandbox

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/cfg"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/cgroup"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/fc"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/network"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/template/metadata"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/sandboxtypes"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

type canceledConstructionTemplate struct {
	capacityOnlyTemplate

	err error
}

func (t canceledConstructionTemplate) Metadata() (metadata.Template, error) {
	return metadata.Template{}, t.err
}

func (canceledConstructionTemplate) Files() storage.CachePaths { return storage.CachePaths{} }

func (t canceledConstructionTemplate) Rootfs() (block.ReadonlyDevice, error) {
	return nil, t.err
}

type canceledConstructionPool struct{ called chan struct{} }

func (p canceledConstructionPool) Get(ctx context.Context, _ *orchestrator.SandboxNetworkConfig, _ sandboxtypes.EgressClass) (*network.Slot, error) {
	close(p.called)

	return nil, ctx.Err()
}

func (canceledConstructionPool) ReturnAsync(context.Context, *network.Slot, network.ReleaseNotify, time.Duration) error {
	panic("canceled construction did not acquire a slot")
}

func (canceledConstructionPool) Close(context.Context) error { return nil }

// Both actual constructor defers must preserve a cancellation cause even when a
// dependency reports only context.Canceled. The template fails before provider,
// UFFD, or Firecracker construction; no network slot or cgroup is acquired.
func TestFactoryFailedConstructorsPreserveCancellationCause(t *testing.T) {
	t.Parallel()
	for _, resume := range []bool{false, true} {
		t.Run(map[bool]string{false: "create", true: "resume"}[resume], func(t *testing.T) {
			t.Parallel()
			cause := errors.New("construction dependency canceled with its operational failure")
			primary := fmt.Errorf("template lookup interrupted: %w", context.Canceled)
			ctx, cancel := context.WithCancelCause(t.Context())
			cancel(cause)
			pool := canceledConstructionPool{called: make(chan struct{})}
			factory := NewFactory(t.Context(), cfg.BuilderConfig{
				StorageConfig: storage.Config{SandboxCacheDir: t.TempDir()},
			}, pool, nil, nil, nil, cgroup.NewNoopManager(), nil, nil, NewSandboxesMap())
			config := NewConfig(Config{Vcpu: 1})
			runtime := sandboxtypes.RuntimeMetadata{SandboxID: uuid.NewString(), ExecutionID: uuid.NewString()}
			template := canceledConstructionTemplate{err: primary}
			var sbx *Sandbox
			var err error
			if resume {
				sbx, err = factory.ResumeSandbox(ctx, template, config, runtime, time.Time{}, time.Time{}, nil)
			} else {
				sbx, err = factory.CreateSandbox(ctx, config, runtime, template, time.Minute, "", fc.ProcessOptions{}, nil, nil)
			}
			if !resume {
				select {
				case <-pool.called:
				case <-time.After(time.Second):
					t.Fatal("owned network lookup did not complete")
				}
			}
			require.Nil(t, sbx)
			require.ErrorIs(t, err, primary)
			require.ErrorIs(t, err, cause, "the actual constructor must retain the cancellation diagnosis")
		})
	}
}

func TestFactoryFailedConstructorsDoNotAttributeIndependentErrorsToCancellation(t *testing.T) {
	t.Parallel()
	for _, resume := range []bool{false, true} {
		t.Run(map[bool]string{false: "create", true: "resume"}[resume], func(t *testing.T) {
			t.Parallel()
			cause := errors.New("construction dependency canceled with its operational failure")
			primary := errors.New("template version is unavailable")
			ctx, cancel := context.WithCancelCause(t.Context())
			cancel(cause)
			pool := canceledConstructionPool{called: make(chan struct{})}
			factory := NewFactory(t.Context(), cfg.BuilderConfig{
				StorageConfig: storage.Config{SandboxCacheDir: t.TempDir()},
			}, pool, nil, nil, nil, cgroup.NewNoopManager(), nil, nil, NewSandboxesMap())
			config := NewConfig(Config{Vcpu: 1})
			runtime := sandboxtypes.RuntimeMetadata{SandboxID: uuid.NewString(), ExecutionID: uuid.NewString()}
			template := canceledConstructionTemplate{err: primary}
			var sbx *Sandbox
			var err error
			if resume {
				sbx, err = factory.ResumeSandbox(ctx, template, config, runtime, time.Time{}, time.Time{}, nil)
			} else {
				sbx, err = factory.CreateSandbox(ctx, config, runtime, template, time.Minute, "", fc.ProcessOptions{}, nil, nil)
			}
			if !resume {
				select {
				case <-pool.called:
				case <-time.After(time.Second):
					t.Fatal("owned network lookup did not complete")
				}
			}
			require.Nil(t, sbx)
			require.ErrorIs(t, err, primary)
			require.NotErrorIs(t, err, cause, "an independent template error must not inherit an unrelated cancellation diagnosis")
		})
	}
}

func TestPreserveConstructionCause(t *testing.T) {
	t.Parallel()
	cause := errors.New("construction dependency failed")
	primary := fmt.Errorf("construction interrupted: %w", context.Canceled)
	for _, tc := range []struct {
		name      string
		cause     error
		primary   error
		cancel    bool
		deadline  bool
		wantCause bool
		unchanged bool
	}{
		{name: "custom cause", cause: cause, primary: primary, cancel: true, wantCause: true},
		{name: "wrapped cancellation diagnosis", cause: fmt.Errorf("provider transport failed: %w", context.Canceled), primary: primary, cancel: true, wantCause: true},
		{name: "wrapped deadline diagnosis", cause: fmt.Errorf("provider deadline failed: %w", context.DeadlineExceeded), primary: fmt.Errorf("construction timed out: %w", context.DeadlineExceeded), deadline: true, wantCause: true},
		{name: "already preserved", cause: cause, primary: fmt.Errorf("construction failed: %w", cause), cancel: true, wantCause: true, unchanged: true},
		{name: "ordinary cancellation", primary: primary, cancel: true, unchanged: true},
		{name: "ordinary deadline", primary: fmt.Errorf("construction timed out: %w", context.DeadlineExceeded), deadline: true, unchanged: true},
		{name: "independent error with canceled context", cause: cause, primary: errors.New("invalid construction request"), cancel: true, unchanged: true},
		{name: "independent error with expired deadline", cause: cause, primary: errors.New("invalid construction request"), deadline: true, unchanged: true},
		{name: "uncanceled", primary: primary, unchanged: true},
		{name: "successful construction", cause: cause, cancel: true, unchanged: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancelCause(t.Context())
			defer cancel(nil)
			if tc.cancel {
				cancel(tc.cause)
			}
			if tc.deadline {
				var stop context.CancelFunc
				ctx, stop = context.WithDeadlineCause(t.Context(), time.Unix(0, 0), tc.cause)
				defer stop()
			}
			got := preserveConstructionCause(ctx, tc.primary)
			if tc.primary != nil {
				require.ErrorIs(t, got, tc.primary)
			} else {
				require.NoError(t, got)
			}
			if tc.wantCause {
				require.ErrorIs(t, got, tc.cause)
			}
			if tc.unchanged {
				require.Equal(t, tc.primary, got)
			}
		})
	}
}
