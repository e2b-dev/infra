package metrics

import (
	"time"

	"github.com/gin-gonic/gin"
	semconv "go.opentelemetry.io/otel/semconv/v1.7.0"
)

// Middleware returns middleware that will trace incoming requests.
// The service parameter should describe the name of the (virtual)
// server handling the request.
func Middleware(service string, options ...Option) gin.HandlerFunc {
	cfg := defaultConfig()
	for _, option := range options {
		option.apply(cfg)
	}

	recorder := cfg.recorder
	if recorder == nil {
		recorder = GetRecorder(service)
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

			duration := time.Since(start)
			recorder.ObserveHTTPRequestDuration(ctx, duration, resAttributes)
		}()

		ginCtx.Next()
	}
}
