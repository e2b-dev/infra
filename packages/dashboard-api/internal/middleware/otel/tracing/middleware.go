package tracing

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin"
	"go.opentelemetry.io/otel/attribute"
	semconv "go.opentelemetry.io/otel/semconv/v1.12.0"
	oteltrace "go.opentelemetry.io/otel/trace"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	sharedmiddleware "github.com/e2b-dev/infra/packages/shared/pkg/middleware"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const (
	tracerKey  = "otel-go-contrib-tracer"
	tracerName = "go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin"
)

type requestStartTimeKey struct{}

func WithRequestStartTime(ctx context.Context, startTime time.Time) context.Context {
	return context.WithValue(ctx, requestStartTimeKey{}, startTime)
}

func GetRequestStartTime(ctx context.Context) (time.Time, bool) {
	startTime, ok := ctx.Value(requestStartTimeKey{}).(time.Time)
	return startTime, ok
}

type config struct {
	TracerProvider oteltrace.TracerProvider
}

func Middleware(tracerProvider oteltrace.TracerProvider, service string) gin.HandlerFunc {
	cfg := config{}
	if cfg.TracerProvider == nil {
		cfg.TracerProvider = tracerProvider
	}

	tracer := cfg.TracerProvider.Tracer(
		tracerName,
		oteltrace.WithInstrumentationVersion(otelgin.Version()),
	)

	return func(c *gin.Context) {
		c.Set(tracerKey, tracer)
		ctx := c.Request.Context()
		ctx = WithRequestStartTime(ctx, time.Now())

		defer func() {
			c.Request = c.Request.WithContext(ctx)
		}()

		if c.Request.Header.Get("traceparent") != "" {
			c.Request.Header.Del("traceparent")
		}

		if edgeTraceID, ok := telemetry.ParseEdgeTraceID(
			c.Request.Header.Get(telemetry.GCPTraceContextHeader),
			c.Request.Header.Get(telemetry.AWSTraceContextHeader),
		); ok {
			ctx = logger.ContextWithEdgeTraceID(ctx, edgeTraceID)
		}

		opts := []oteltrace.SpanStartOption{
			oteltrace.WithAttributes(semconv.NetAttributesFromHTTPRequest("tcp", c.Request)...),
			oteltrace.WithAttributes(semconv.EndUserAttributesFromHTTPRequest(c.Request)...),
			oteltrace.WithAttributes(semconv.HTTPServerAttributesFromHTTPRequest(service, c.FullPath(), c.Request)...),
			oteltrace.WithSpanKind(oteltrace.SpanKindServer),
		}

		spanName := c.FullPath()
		if spanName == "" {
			spanName = fmt.Sprintf("HTTP %s route not found", c.Request.Method)
		}

		ctx, span := tracer.Start(ctx, spanName, opts...)
		defer span.End()

		c.Request = c.Request.WithContext(ctx)
		c.Next()

		status := c.Writer.Status()
		cause := sharedmiddleware.CancelCause(c)
		if errors.Is(cause, sharedmiddleware.ErrRequestTimeout) {
			span.SetAttributes(attribute.Bool("request.timeout", true))
		} else if errors.Is(cause, context.Canceled) {
			status = sharedmiddleware.StatusClientClosedRequest
			span.SetAttributes(attribute.Bool("client.canceled", true))
		}

		attrs := semconv.HTTPAttributesFromHTTPStatusCode(status)
		span.SetAttributes(attrs...)
		spanStatus, spanMessage := semconv.SpanStatusFromHTTPStatusCode(status)
		span.SetStatus(spanStatus, spanMessage)

		if len(c.Errors) > 0 {
			span.SetAttributes(attribute.String("gin.errors", strings.TrimSpace(c.Errors.String())))
		}
	}
}
