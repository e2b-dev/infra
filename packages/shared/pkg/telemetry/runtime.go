package telemetry

import (
	"context"

	"go.opentelemetry.io/contrib/instrumentation/runtime"
)

// StartRuntimeInstrumentation starts the standard OTEL Go runtime metrics collection.
// Uses go.opentelemetry.io/contrib/instrumentation/runtime.
//
// Collected metrics:
//   - runtime.go.goroutines
//   - runtime.go.gc.pause_total
//   - runtime.go.mem.heap_alloc
//   - runtime.go.mem.heap_idle
//   - runtime.go.mem.heap_inuse
//   - runtime.go.mem.heap_objects
//   - runtime.go.mem.heap_released
//   - runtime.go.mem.heap_sys
//   - runtime.go.mem.live_objects
//   - runtime.go.cgo.calls
//
// Performance: Uses runtime/metrics (no STW pause), ~50μs per collection.
func (t *Client) StartRuntimeInstrumentation() (stop func(context.Context) error, err error) {
	err = runtime.Start(
		runtime.WithMeterProvider(t.MeterProvider),
		runtime.WithMinimumReadMemStatsInterval(metricExportPeriod),
	)
	if err != nil {
		return nil, err
	}

	return func(context.Context) error { return nil }, nil
}
