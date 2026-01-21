package telemetry

import (
	"context"
	"fmt"
	"os"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

var OTELTracingPrint = os.Getenv("OTEL_TRACING_PRINT") != "false"

func debugFormat(debugID *string, msg string) string {
	if debugID == nil {
		return msg
	}

	return fmt.Sprintf("[%s] %s", *debugID, msg)
}

func WithAttributes(ctx context.Context, attrs ...attribute.KeyValue) context.Context {
	span := trace.SpanFromContext(ctx)

	if OTELTracingPrint {
		var msg string

		if len(attrs) == 0 {
			msg = "No attrs set"
		} else {
			msg = fmt.Sprintf("Attrs set: %#v\n", attrs)
		}

		debugID := logger.GetDebugID(ctx)
		fmt.Print(debugFormat(debugID, msg))
	}

	// Catch special attributes to set in context so they are available in child spans
	for _, attr := range attrs {
		switch string(attr.Key) {
		case logger.SandboxIDContextKey:
			ctx = context.WithValue(ctx, logger.SandboxIDContextKey, attr.Value.AsString()) //nolint:fatcontext,staticcheck // intentionally updating context in loop
		case logger.TeamIDIDContextKey:
			ctx = context.WithValue(ctx, logger.TeamIDIDContextKey, attr.Value.AsString()) //nolint:staticcheck
		case logger.BuildIDContextKey:
			ctx = context.WithValue(ctx, logger.BuildIDContextKey, attr.Value.AsString()) //nolint:staticcheck
		case logger.TemplateIDContextKey:
			ctx = context.WithValue(ctx, logger.TemplateIDContextKey, attr.Value.AsString()) //nolint:staticcheck
		}
	}

	span.SetAttributes(attrs...)

	return ctx
}

func ReportEvent(ctx context.Context, name string, attrs ...attribute.KeyValue) {
	span := trace.SpanFromContext(ctx)

	if OTELTracingPrint {
		var msg string

		if len(attrs) == 0 {
			msg = fmt.Sprintf("-> %s\n", name)
		} else {
			msg = fmt.Sprintf("-> %s - %#v\n", name, attrs)
		}

		debugID := logger.GetDebugID(ctx)
		fmt.Print(debugFormat(debugID, msg))
	}

	span.AddEvent(name,
		trace.WithAttributes(attrs...),
	)
}

func ReportCriticalError(ctx context.Context, message string, err error, attrs ...attribute.KeyValue) {
	span := trace.SpanFromContext(ctx)

	attrs = append(attrs, attribute.String("error.message", message))
	attrs = append(attrs, AttributesFromContext(ctx)...)

	span.SetAttributes(attrs...)
	span.RecordError(fmt.Errorf("%s: %w", message, err),
		trace.WithStackTrace(true),
		trace.WithAttributes(attrs...),
	)

	span.SetStatus(codes.Error, message)
}

func ReportError(ctx context.Context, message string, err error, attrs ...attribute.KeyValue) {
	span := trace.SpanFromContext(ctx)

	attrs = append(attrs, AttributesFromContext(ctx)...)

	span.SetAttributes(attrs...)
	span.RecordError(fmt.Errorf("%s: %w", message, err),
		trace.WithStackTrace(true),
		trace.WithAttributes(attrs...),
	)
}
