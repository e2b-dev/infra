//go:build linux

package nbd

import (
	"testing"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// testSpanRecorder collects the package's spans. Close builds its span from the
// package-level tracer, which resolves against the global provider, so the
// provider has to be installed globally rather than passed in -- and only once,
// because otel's global instruments delegate on the first SetTracerProvider.
var testSpanRecorder = tracetest.NewSpanRecorder()

func TestMain(m *testing.M) {
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(testSpanRecorder)))
	m.Run()
}
