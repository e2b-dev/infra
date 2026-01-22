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

func SetAttributes(ctx context.Context, attrs ...attribute.KeyValue) {
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

	span.SetAttributes(attrs...)
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

	span.SetAttributes(attrs...)
	span.RecordError(fmt.Errorf("%s: %w", message, err),
		trace.WithStackTrace(true),
		trace.WithAttributes(attrs...),
	)

	span.SetStatus(codes.Error, message)
}

func ReportError(ctx context.Context, message string, err error, attrs ...attribute.KeyValue) {
	span := trace.SpanFromContext(ctx)

	span.SetAttributes(attrs...)
	span.RecordError(fmt.Errorf("%s: %w", message, err),
		trace.WithStackTrace(true),
		trace.WithAttributes(attrs...),
	)
}
