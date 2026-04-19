package telemetry_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

// newTestTracer returns a tracer that records spans in an in-memory exporter
// so tests can inspect Links and other span data.
func newTestTracer(t *testing.T) (trace.Tracer, *tracetest.InMemoryExporter) {
	t.Helper()

	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	// Use the W3C propagator matching production.
	otel.SetTextMapPropagator(telemetry.NewTextPropagator())

	return tp.Tracer("test"), exp
}

func TestLinkSpans_NoOpOnInvalidPrimary(t *testing.T) {
	t.Parallel()
	tracer, exp := newTestTracer(t)

	ctx, span := tracer.Start(t.Context(), "caller")
	telemetry.LinkSpans(ctx, trace.SpanContext{}) // zero value
	span.End()

	require.Len(t, exp.GetSpans(), 1)
	assert.Empty(t, exp.GetSpans()[0].Links)
}

func TestLinkSpans_NoOpOnNoCallerSpan(t *testing.T) {
	t.Parallel()
	tracer, _ := newTestTracer(t)

	// Create a valid primary SpanContext.
	_, primary := tracer.Start(t.Context(), "primary")
	defer primary.End()

	// Caller ctx has no span — should no-op without panic.
	telemetry.LinkSpans(t.Context(), primary.SpanContext())
}

func TestLinkSpans_AddsLink(t *testing.T) {
	t.Parallel()
	tracer, exp := newTestTracer(t)

	_, primary := tracer.Start(t.Context(), "primary")
	primarySC := primary.SpanContext()
	primary.End()

	callerCtx, caller := tracer.Start(t.Context(), "caller")
	telemetry.LinkSpans(callerCtx, primarySC)
	caller.End()

	var callerSpan tracetest.SpanStub
	for _, s := range exp.GetSpans() {
		if s.Name == "caller" {
			callerSpan = s
		}
	}

	require.Len(t, callerSpan.Links, 1)
	assert.Equal(t, primarySC.TraceID(), callerSpan.Links[0].SpanContext.TraceID())
	assert.Equal(t, primarySC.SpanID(), callerSpan.Links[0].SpanContext.SpanID())
}

func TestInjectTraceparent_EmptyOnNoSpan(t *testing.T) {
	t.Parallel()
	newTestTracer(t) // set propagator

	assert.Empty(t, telemetry.InjectTraceparent(t.Context()))
}

func TestInjectTraceparent_RoundTrip(t *testing.T) {
	t.Parallel()
	tracer, _ := newTestTracer(t)

	ctx, span := tracer.Start(t.Context(), "primary")
	defer span.End()

	tp := telemetry.InjectTraceparent(ctx)
	require.NotEmpty(t, tp)

	sc := telemetry.ExtractPrimarySpanContext(tp)
	require.True(t, sc.IsValid())
	assert.Equal(t, span.SpanContext().TraceID(), sc.TraceID())
	assert.Equal(t, span.SpanContext().SpanID(), sc.SpanID())
}

func TestExtractPrimarySpanContext_EmptyAndGarbage(t *testing.T) {
	t.Parallel()
	newTestTracer(t)

	assert.False(t, telemetry.ExtractPrimarySpanContext("").IsValid())
	assert.False(t, telemetry.ExtractPrimarySpanContext("not-a-traceparent").IsValid())
}

func TestInjectTraceparent_NoAllocsOnNoSpan(t *testing.T) {
	// Not parallel: AllocsPerRun requires exclusive access to the runtime.
	newTestTracer(t)

	ctx := t.Context()
	allocs := testing.AllocsPerRun(100, func() {
		_ = telemetry.InjectTraceparent(ctx)
	})
	assert.Equal(t, 0.0, allocs, "InjectTraceparent should not allocate when ctx has no span")
}
