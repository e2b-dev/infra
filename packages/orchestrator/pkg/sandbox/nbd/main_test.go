//go:build linux

package nbd

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

// testSpanRecorder collects the package's spans. Close builds its span from the
// package-level tracer, which resolves against the global provider, so the
// provider has to be installed globally rather than passed in -- and only once,
// because otel's global instruments delegate on the first SetTracerProvider.
var testSpanRecorder = tracetest.NewSpanRecorder()

// testMetricReader and testLogObserver capture the package's instruments and
// warn logs the same way: both resolve against process-wide globals, so the
// test doubles are installed once here and every test filters what it reads.
var (
	testMetricReader = sdkmetric.NewManualReader()
	testLogObserver  *observer.ObservedLogs
)

func TestMain(m *testing.M) {
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(testSpanRecorder)))
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(testMetricReader)))

	observerCore, logs := observer.New(zap.InfoLevel)
	testLogObserver = logs
	logger.ReplaceGlobals(context.Background(), logger.NewTracedLoggerFromCore(observerCore))

	m.Run()
}
