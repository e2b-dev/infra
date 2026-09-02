//go:build linux

package build

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/template/build/builderrors"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/template/build/metrics"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/template/build/phases"
)

func TestClassifyBuildResult(t *testing.T) {
	t.Parallel()

	userErr := phases.NewPhaseBuildError(phases.PhaseMeta{}, errors.New("exit status 2"))

	tests := []struct {
		name string
		r    *Result
		err  error
		want metrics.BuildResultType
	}{
		{name: "success", r: &Result{}, err: nil, want: metrics.BuildResultSuccess},
		{name: "nil result without error is internal", r: nil, err: nil, want: metrics.BuildResultInternalError},
		{name: "plain error is internal", err: errors.New("mmap memfd: cannot allocate memory"), want: metrics.BuildResultInternalError},
		{name: "phase build error is user", err: userErr, want: metrics.BuildResultUserError},
		{
			name: "wrapped phase build error is user",
			err:  fmt.Errorf("error while building template: %w", fmt.Errorf("error building step 10: %w", userErr)),
			want: metrics.BuildResultUserError,
		},
		{
			name: "user cancellation is user",
			err:  phases.NewPhaseBuildError(phases.PhaseMeta{}, builderrors.ErrCanceled),
			want: metrics.BuildResultUserError,
		},
		{
			name: "child-context timeout without build-level cancellation is internal",
			err:  fmt.Errorf("wait for envd: %w", context.DeadlineExceeded),
			want: metrics.BuildResultInternalError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, ClassifyBuildResult(tt.r, tt.err))
		})
	}
}

// TestBuildResultAttribute_MatchesMetricLabel pins the contract that the span
// attribute carries exactly the metric label value, so a TraceQL filter on
// build.result and a PromQL filter on result select the same builds.
func TestBuildResultAttribute_MatchesMetricLabel(t *testing.T) {
	t.Parallel()

	for _, rt := range []metrics.BuildResultType{
		metrics.BuildResultSuccess,
		metrics.BuildResultUserError,
		metrics.BuildResultInternalError,
	} {
		kv := metrics.BuildResultAttribute(rt)
		assert.Equal(t, metrics.BuildResultAttributeKey, kv.Key)
		assert.Equal(t, string(rt), kv.Value.AsString())
	}
}

// TestBuildResultAttribute_SetOnParentAndChild exercises the span plumbing
// used by Builder.Build: capture the caller's span, open a child, and stamp the
// same attribute on both so a filter on either span finds the build.
func TestBuildResultAttribute_SetOnParentAndChild(t *testing.T) {
	t.Parallel()

	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	tr := tp.Tracer("github.com/e2b-dev/infra/packages/orchestrator/pkg/template/build")

	ctx, parent := tr.Start(t.Context(), "template-background-build")
	func(ctx context.Context) {
		parentSpan := trace.SpanFromContext(ctx)
		_, child := tr.Start(ctx, "build")
		defer child.End()

		attr := metrics.BuildResultAttribute(metrics.BuildResultUserError)
		child.SetAttributes(attr)
		parentSpan.SetAttributes(attr)
	}(ctx)
	parent.End()

	ended := rec.Ended()
	require.Len(t, ended, 2)
	for _, s := range ended {
		got, ok := findAttr(s.Attributes(), metrics.BuildResultAttributeKey)
		require.True(t, ok, "span %q missing build.result", s.Name())
		assert.Equal(t, "user_error", got.AsString(), "span %q", s.Name())
	}
}

func findAttr(attrs []attribute.KeyValue, key attribute.Key) (attribute.Value, bool) {
	for _, kv := range attrs {
		if kv.Key == key {
			return kv.Value, true
		}
	}

	return attribute.Value{}, false
}
