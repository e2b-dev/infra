package telemetry

import (
	"go.opentelemetry.io/contrib/instrumentation/runtime"
)

// StartRuntimeInstrumentation registers OTEL Go runtime metric callbacks.
//
// Collected metrics (semantic-convention names):
//   - go.memory.used
//   - go.memory.limit
//   - go.memory.allocated
//   - go.memory.allocations
//   - go.memory.gc.goal
//   - go.goroutine.count
//   - go.processor.limit
//   - go.config.gogc
//
// The callbacks are invoked by the MeterProvider and stop automatically
// when it shuts down — no separate goroutine is spawned.
func (t *Client) StartRuntimeInstrumentation() error {
	return runtime.Start(
		runtime.WithMeterProvider(t.MeterProvider),
		runtime.WithMinimumReadMemStatsInterval(metricExportPeriod),
	)
}
