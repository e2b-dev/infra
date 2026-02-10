package telemetry

import (
	"context"
	"fmt"
	"net/http"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

func SetAttributes(ctx context.Context, attrs ...attribute.KeyValue) {
	span := trace.SpanFromContext(ctx)
	span.SetAttributes(attrs...)
}

func ReportEvent(ctx context.Context, name string, attrs ...attribute.KeyValue) {
	span := trace.SpanFromContext(ctx)
	span.AddEvent(name,
		trace.WithAttributes(attrs...),
	)
}

func ReportCriticalError(ctx context.Context, message string, err error, attrs ...attribute.KeyValue) {
	span := trace.SpanFromContext(ctx)

	logger.L().With(attributesToZapFields(attrs...)...).Error(ctx, message, zap.Error(err))

	errorAttrs := append(attrs, attribute.String("error.message", message))

	span.RecordError(fmt.Errorf("%s: %w", message, err),
		trace.WithStackTrace(true),
		trace.WithAttributes(
			errorAttrs...,
		),
	)

	span.SetStatus(codes.Error, message)
}

func ReportError(ctx context.Context, message string, err error, attrs ...attribute.KeyValue) {
	span := trace.SpanFromContext(ctx)

	logger.L().With(attributesToZapFields(attrs...)...).Warn(ctx, message, zap.Error(err))

	span.RecordError(fmt.Errorf("%s: %w", message, err),
		trace.WithStackTrace(true),
		trace.WithAttributes(
			attrs...,
		),
	)
}

func ReportErrorByCode(ctx context.Context, code int, message string, err error, attrs ...attribute.KeyValue) {
	if code >= http.StatusInternalServerError {
		ReportCriticalError(ctx, message, err, attrs...)
	} else {
		ReportError(ctx, message, err, attrs...)
	}
}

func attributesToZapFields(attrs ...attribute.KeyValue) []zap.Field {
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
