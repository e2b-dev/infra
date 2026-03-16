package tracing

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

var tracer = otel.Tracer("github.com/e2b-dev/infra/packages/orchestrator/internal/nfsproxy/tracing")

func startSpan(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, func(error, ...attribute.KeyValue)) {
	ctx, span := tracer.Start(ctx, name, trace.WithAttributes(attrs...))

	return ctx, func(err error, endAttrs ...attribute.KeyValue) {
		if len(endAttrs) > 0 {
			span.SetAttributes(endAttrs...)
		}
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
		span.End()
	}
}
