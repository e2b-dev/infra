package tracing

import (
	"context"
	"errors"
	"os"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

var tracer = otel.Tracer("github.com/e2b-dev/infra/packages/orchestrator/pkg/nfsproxy/tracing")

type finishFunc func(error, ...attribute.KeyValue)

func startSpan(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, finishFunc) {
	ctx, span := tracer.Start(ctx, name, trace.WithAttributes(attrs...)) //nolint:spancheck // span.End called by returned function

	return ctx, func(err error, endAttrs ...attribute.KeyValue) { //nolint:spancheck // must be called by caller
		if len(endAttrs) > 0 {
			span.SetAttributes(endAttrs...)
		}
		if err != nil {
			span.RecordError(err)
			if !isUserError(err) {
				span.SetStatus(codes.Error, err.Error())
			}
		}
		span.End()
	}
}

func isUserError(err error) bool {
	if errors.Is(err, os.ErrNotExist) {
		return true
	}

	if errors.Is(err, os.ErrExist) {
		return true
	}

	return false
}
