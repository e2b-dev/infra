package telemetry

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

// LinkSpans adds a trace.Link from the caller's active span to the
// primary SpanContext. No-op when either span is invalid.
//
// Safe to call on hot paths: it is one map lookup (SpanFromContext) plus two
// validity checks when there is no link to add.
func LinkSpans(ctx context.Context, primary trace.SpanContext) {
	if !primary.IsValid() {
		return
	}

	caller := trace.SpanFromContext(ctx)
	if !caller.SpanContext().IsValid() {
		return
	}

	caller.AddLink(trace.Link{SpanContext: primary})
}

// traceparentHeader is the trace-context header name. Declared once so
// the single-slot carrier below has one source of truth.
const traceparentHeader = "traceparent"

// traceparentCarrier is a TextMapCarrier used to serialize a traceparent string
// without the per-call map allocation that propagation.MapCarrier forces.
// Only the "traceparent" header is carried — tracestate is intentionally dropped.
// We don't emit it today and it is not required for span linking.
type traceparentCarrier [1]string

func (c *traceparentCarrier) Get(key string) string {
	if key == traceparentHeader {
		return c[0]
	}

	return ""
}

func (c *traceparentCarrier) Set(key, value string) {
	if key == traceparentHeader {
		c[0] = value
	}
}

func (c *traceparentCarrier) Keys() []string { return []string{traceparentHeader} }

// InjectTraceparent returns the caller's span as traceparent string.
func InjectTraceparent(ctx context.Context) string {
	if !trace.SpanContextFromContext(ctx).IsValid() {
		return ""
	}

	var c traceparentCarrier
	otel.GetTextMapPropagator().Inject(ctx, &c)

	return c.Get(traceparentHeader)
}

// ExtractPrimarySpanContext returns the embedded SpanContext with traceparent.
// Callers pass the result directly to LinkSpans.
func ExtractPrimarySpanContext(traceparent string) trace.SpanContext {
	if traceparent == "" {
		return trace.SpanContext{}
	}

	c := traceparentCarrier{traceparent}
	extracted := otel.GetTextMapPropagator().Extract(context.Background(), &c)

	return trace.SpanContextFromContext(extracted)
}
