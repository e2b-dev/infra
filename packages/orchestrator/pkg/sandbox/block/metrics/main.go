package metrics

import (
	"fmt"

	"go.opentelemetry.io/otel/metric"

	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const orchestratorChunkSlice = "orchestrator.chunk.slice"

type Metrics struct {
	ChunkSliceTimerFactory telemetry.FloatTimerFactory
}

func NewMetrics(meterProvider metric.MeterProvider) (Metrics, error) {
	var m Metrics

	blocksMeter := meterProvider.Meter("github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block/metrics")

	var err error
	if m.ChunkSliceTimerFactory, err = telemetry.NewFloatTimerFactory(
		blocksMeter, orchestratorChunkSlice,
		"Time taken by Chunker to serve a Slice() (source=mmap when served from cache)",
		"Bytes returned",
	); err != nil {
		return m, fmt.Errorf("error creating chunk slice timer factory: %w", err)
	}

	return m, nil
}
