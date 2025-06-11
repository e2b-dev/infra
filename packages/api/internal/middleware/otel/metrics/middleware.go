package metrics

import (
	"fmt"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	semconv "go.opentelemetry.io/otel/semconv/v1.7.0"
)

const MetricPrefix = "metric."

// Middleware returns middleware that will trace incoming requests.
// The service parameter should describe the name of the (virtual)
// server handling the request.
func Middleware(meter metric.Meter, service string, options ...Option) gin.HandlerFunc {
	cfg := defaultConfig()
	for _, option := range options {
		option.apply(cfg)
	}

	recorder := cfg.recorder
	if recorder == nil {
		recorder = GetRecorder(meter, service)
	}

	return func(ginCtx *gin.Context) {
		ctx := ginCtx.Request.Context()

		route := ginCtx.FullPath()
		if len(route) <= 0 {
			route = "nonconfigured"
		}

		if !cfg.shouldRecord(service, route, ginCtx.Request) {
			ginCtx.Next()

			return
		}

		start := time.Now()
		reqAttributes := cfg.attributes(service, route, ginCtx.Request)

		defer func() {
			resAttributes := append(
				reqAttributes[0:0],
				reqAttributes...,
			)

			if cfg.groupedStatus {
				code := int(ginCtx.Writer.Status()/100) * 100
				resAttributes = append(resAttributes, semconv.HTTPStatusCodeKey.Int(code))
			} else {
				resAttributes = append(resAttributes, semconv.HTTPAttributesFromHTTPStatusCode(ginCtx.Writer.Status())...)
			}

			// Append attributes from ginCtx
			resAttributes = append(resAttributes, attributesFromGinContext(ginCtx, MetricPrefix)...)

			duration := time.Since(start)
			recorder.ObserveHTTPRequestDuration(ctx, duration, resAttributes)
		}()

		ginCtx.Next()
	}
}

func attributesFromGinContext(ginCtx *gin.Context, filterPrefix string) []attribute.KeyValue {
	attrs := make([]attribute.KeyValue, 0, len(ginCtx.Keys))
	for keyRaw, v := range ginCtx.Keys {
		if !strings.HasPrefix(keyRaw, filterPrefix) {
			continue
		}
		k := strings.TrimPrefix(keyRaw, filterPrefix)
		switch val := v.(type) {
		case string:
			attrs = append(attrs, attribute.String(k, val))
		case int:
			attrs = append(attrs, attribute.Int(k, val))
		case bool:
			attrs = append(attrs, attribute.Bool(k, val))
		default:
			attrs = append(attrs, attribute.String(k, fmt.Sprintf("%v", val)))
		}
	}
	return attrs
}
