package telemetry

import (
	"context"
	"fmt"
	"os"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

var OTELTracingPrint = os.Getenv("OTEL_TRACING_PRINT") != "false"

const DebugID = "debug_id"

func getDebugID(ctx context.Context) *string {
	if ctx.Value(DebugID) == nil {
		return nil
	}

	value := ctx.Value(DebugID).(string)

	return &value
}

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

		debugID := getDebugID(ctx)
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

		debugID := getDebugID(ctx)
		fmt.Print(debugFormat(debugID, msg))
	}

	span.AddEvent(name,
		trace.WithAttributes(attrs...),
	)
}

func ReportCriticalError(ctx context.Context, message string, err error, attrs ...attribute.KeyValue) {
	span := trace.SpanFromContext(ctx)

	debugID := getDebugID(ctx)
	zap.L().Error(message, zap.Stringp("debug_id", debugID), zap.Error(err), zap.Any("attrs", attrs))

	errorAttrs := append(attrs, attribute.String("error.message", message))

	span.RecordError(err,
		trace.WithStackTrace(true),
		trace.WithAttributes(
			errorAttrs...,
		),
	)

	span.SetStatus(codes.Error, message)
}

func ReportError(ctx context.Context, message string, err error, attrs ...attribute.KeyValue) {
	span := trace.SpanFromContext(ctx)

	debugID := getDebugID(ctx)
	zap.L().Warn(message, zap.Stringp("debug_id", debugID), zap.Error(err), zap.Any("attrs", attrs))

	span.RecordError(err,
		trace.WithStackTrace(true),
		trace.WithAttributes(
			attrs...,
		),
	)
}

func GetContextFromRemote(ctx context.Context, tracer trace.Tracer, name, spanID, traceID string) (context.Context, trace.Span) {
	tid, traceIDErr := trace.TraceIDFromHex(traceID)
	if traceIDErr != nil {
		ReportError(
			ctx,
			traceIDErr.Error(),
			traceIDErr,
			attribute.String("trace.id", traceID),
			attribute.Int("trace.id.length", len(traceID)),
		)
	}

	sid, spanIDErr := trace.SpanIDFromHex(spanID)
	if spanIDErr != nil {
		ReportError(
			ctx,
			spanIDErr.Error(),
			spanIDErr,
			attribute.String("span.id", spanID),
			attribute.Int("span.id.length", len(spanID)),
		)
	}

	remoteCtx := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    tid,
		SpanID:     sid,
		TraceFlags: 0x0,
	})

	return tracer.Start(
		trace.ContextWithRemoteSpanContext(ctx, remoteCtx),
		name,
		trace.WithLinks(
			trace.LinkFromContext(ctx, attribute.String("link", "validation")),
		),
	)
}
