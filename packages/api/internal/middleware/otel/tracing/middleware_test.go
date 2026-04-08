package tracing

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	oteltrace "go.opentelemetry.io/otel/trace"
)

func TestMiddlewarePropagatesTraceparent(t *testing.T) {
	t.Setenv("GIN_MODE", gin.TestMode)
	gin.SetMode(gin.TestMode)

	propagator := propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{})
	previousPropagator := otel.GetTextMapPropagator()
	otel.SetTextMapPropagator(propagator)
	defer otel.SetTextMapPropagator(previousPropagator)

	spanRecorder := tracetest.NewSpanRecorder()
	tracerProvider := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithSpanProcessor(spanRecorder),
	)

	router := gin.New()
	var observedTraceparent string
	router.Use(Middleware(tracerProvider, "test-service"))
	router.GET("/widgets", func(c *gin.Context) {
		observedTraceparent = c.Request.Header.Get("traceparent")
		c.Status(http.StatusNoContent)
	})

	parentSpanContext := oteltrace.NewSpanContext(oteltrace.SpanContextConfig{
		TraceID:    oteltrace.TraceID{0x10, 0x32, 0x54, 0x76, 0x98, 0xba, 0xdc, 0xfe, 0x10, 0x32, 0x54, 0x76, 0x98, 0xba, 0xdc, 0xfe},
		SpanID:     oteltrace.SpanID{0x01, 0x23, 0x45, 0x67, 0x89, 0xab, 0xcd, 0xef},
		TraceFlags: oteltrace.FlagsSampled,
		Remote:     true,
	})
	parentCtx := oteltrace.ContextWithRemoteSpanContext(context.Background(), parentSpanContext)

	req := httptest.NewRequest(http.MethodGet, "/widgets", nil)
	propagator.Inject(parentCtx, propagation.HeaderCarrier(req.Header))

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	require.Equal(t, http.StatusNoContent, recorder.Code)
	require.NotEmpty(t, observedTraceparent)

	spans := spanRecorder.Ended()
	require.Len(t, spans, 1)
	require.Equal(t, parentSpanContext.TraceID(), spans[0].SpanContext().TraceID())
	require.Equal(t, parentSpanContext, spans[0].Parent())
	require.True(t, spans[0].Parent().IsRemote())
}
