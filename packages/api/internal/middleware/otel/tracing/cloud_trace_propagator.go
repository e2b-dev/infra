package tracing

import (
	"context"
	"encoding/binary"
	"strconv"
	"strings"

	"go.opentelemetry.io/otel/propagation"
	oteltrace "go.opentelemetry.io/otel/trace"
)

const cloudTraceContextHeader = "X-Cloud-Trace-Context"

type cloudTracePropagator struct{}

func (cloudTracePropagator) Inject(context.Context, propagation.TextMapCarrier) {}

func (cloudTracePropagator) Extract(ctx context.Context, carrier propagation.TextMapCarrier) context.Context {
	traceID, spanID, traceFlags, ok := parseCloudTraceContext(carrier.Get(cloudTraceContextHeader))
	if !ok {
		return ctx
	}

	spanContext := oteltrace.NewSpanContext(oteltrace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: traceFlags,
		Remote:     true,
	})
	if !spanContext.IsValid() {
		return ctx
	}

	return oteltrace.ContextWithRemoteSpanContext(ctx, spanContext)
}

func (cloudTracePropagator) Fields() []string {
	return []string{cloudTraceContextHeader}
}

func parseCloudTraceContext(header string) (oteltrace.TraceID, oteltrace.SpanID, oteltrace.TraceFlags, bool) {
	if header == "" {
		return oteltrace.TraceID{}, oteltrace.SpanID{}, 0, false
	}

	traceFields := strings.SplitN(header, "/", 2)
	if len(traceFields) != 2 {
		return oteltrace.TraceID{}, oteltrace.SpanID{}, 0, false
	}

	traceID, err := oteltrace.TraceIDFromHex(traceFields[0])
	if err != nil {
		return oteltrace.TraceID{}, oteltrace.SpanID{}, 0, false
	}

	spanAndOptions := strings.SplitN(traceFields[1], ";", 2)
	spanIDInt, err := strconv.ParseUint(spanAndOptions[0], 10, 64)
	if err != nil {
		return oteltrace.TraceID{}, oteltrace.SpanID{}, 0, false
	}

	var spanID oteltrace.SpanID
	binary.BigEndian.PutUint64(spanID[:], spanIDInt)

	traceFlags := oteltrace.TraceFlags(0)
	if len(spanAndOptions) == 2 && strings.Contains(spanAndOptions[1], "o=1") {
		traceFlags = oteltrace.FlagsSampled
	}

	return traceID, spanID, traceFlags, true
}
