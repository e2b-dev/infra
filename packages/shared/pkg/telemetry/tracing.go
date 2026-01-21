package telemetry

import (
	"context"
	"fmt"
	"os"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

var OTELTracingPrint = os.Getenv("OTEL_TRACING_PRINT") != "false"

const (
	DebugID              = "debug_id"
	SandboxIDContextKey  = "sandbox.id"
	TeamIDIDContextKey   = "tema.id"
	BuildIDContextKey    = "build.id"
	TemplateIDContextKey = "template.id"
)

func GetDebugID(ctx context.Context) *string {
	if ctx.Value(DebugID) == nil {
		return nil
	}

	value := ctx.Value(DebugID).(string)

	return &value
}

// GetSandboxID retrieves the sandbox ID from context if present.
func GetSandboxID(ctx context.Context) *string {
	if ctx.Value(SandboxIDContextKey) == nil {
		return nil
	}

	value := ctx.Value(SandboxIDContextKey).(string)

	return &value
}

func getTeamID(ctx context.Context) *string {
	if ctx.Value(TeamIDIDContextKey) == nil {
		return nil
	}

	value := ctx.Value(TeamIDIDContextKey).(string)

	return &value
}

func getBuildID(ctx context.Context) *string {
	if ctx.Value(BuildIDContextKey) == nil {
		return nil
	}

	value := ctx.Value(BuildIDContextKey).(string)

	return &value
}

func getTemplateID(ctx context.Context) *string {
	if ctx.Value(TemplateIDContextKey) == nil {
		return nil
	}

	value := ctx.Value(TemplateIDContextKey).(string)

	return &value
}

func debugFormat(debugID *string, msg string) string {
	if debugID == nil {
		return msg
	}

	return fmt.Sprintf("[%s] %s", *debugID, msg)
}

func SetAttributes(ctx context.Context, attrs ...attribute.KeyValue) context.Context {
	return SetAttributesWithGin(nil, ctx, attrs...)
}

func SetAttributesWithGin(c *gin.Context, ctx context.Context, attrs ...attribute.KeyValue) context.Context {
	span := trace.SpanFromContext(ctx)

	if OTELTracingPrint {
		var msg string

		if len(attrs) == 0 {
			msg = "No attrs set"
		} else {
			msg = fmt.Sprintf("Attrs set: %#v\n", attrs)
		}

		debugID := GetDebugID(ctx)
		fmt.Print(debugFormat(debugID, msg))
	}

	setCtxValueFn := func(ctx context.Context, key, value string) context.Context {
		ctx = context.WithValue(ctx, key, value)

		if c != nil {
			c.Set(key, value)
		}

		return ctx
	}

	// Catch special attributes to set in context so they are available in child spans
	for _, attr := range attrs {
		switch attr.Key {
		case SandboxIDContextKey:
			ctx = setCtxValueFn(ctx, SandboxIDContextKey, attr.Value.AsString())
		case TeamIDIDContextKey:
			ctx = setCtxValueFn(ctx, TeamIDIDContextKey, attr.Value.AsString())
		case BuildIDContextKey:
			ctx = setCtxValueFn(ctx, BuildIDContextKey, attr.Value.AsString())
		case TemplateIDContextKey:
			ctx = setCtxValueFn(ctx, TemplateIDContextKey, attr.Value.AsString())
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

		debugID := GetDebugID(ctx)
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

func AttributesFromContext(ctx context.Context) []attribute.KeyValue {
	var attrs []attribute.KeyValue

	if sandboxID := GetSandboxID(ctx); sandboxID != nil {
		attrs = append(attrs, WithSandboxID(*sandboxID))
	}

	if teamID := getTeamID(ctx); teamID != nil {
		attrs = append(attrs, WithTeamID(*teamID))
	}

	if buildID := getBuildID(ctx); buildID != nil {
		attrs = append(attrs, WithBuildID(*buildID))
	}

	if templateID := getTemplateID(ctx); templateID != nil {
		attrs = append(attrs, WithTemplateID(*templateID))
	}

	return attrs
}

func AttributesToZapFields(attrs ...attribute.KeyValue) []zap.Field {
	fields := make([]zap.Field, 0, len(attrs))
	for _, attr := range attrs {
		key := string(attr.Key)
		switch attr.Value.Type() {
		case attribute.STRING:
			fields = append(fields, zap.String(key, attr.Value.AsString()))
		case attribute.INT64:
			fields = append(fields, zap.Int64(key, attr.Value.AsInt64()))
		case attribute.FLOAT64:
			fields = append(fields, zap.Float64(key, attr.Value.AsFloat64()))
		case attribute.BOOL:
			fields = append(fields, zap.Bool(key, attr.Value.AsBool()))
		case attribute.BOOLSLICE:
			fields = append(fields, zap.Bools(key, attr.Value.AsBoolSlice()))
		case attribute.INT64SLICE:
			fields = append(fields, zap.Int64s(key, attr.Value.AsInt64Slice()))
		case attribute.FLOAT64SLICE:
			fields = append(fields, zap.Float64s(key, attr.Value.AsFloat64Slice()))
		case attribute.STRINGSLICE:
			fields = append(fields, zap.Strings(key, attr.Value.AsStringSlice()))
		default:
			fields = append(fields, zap.Any(key, attr.Value.AsInterface()))
		}
	}

	return fields
}
