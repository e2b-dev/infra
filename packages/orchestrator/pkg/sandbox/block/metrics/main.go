package metrics

import (
	"fmt"

	"go.opentelemetry.io/otel/metric"

	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const (
	orchestratorBlockChunksStore = "orchestrator.blocks.chunks.store"
	orchestratorChunkSlice       = "orchestrator.chunk.slice"
)

type Metrics struct {
	// WriteChunksMetric is used to measure performance of writing chunks to disk.
	WriteChunksTimerFactory telemetry.TimerFactory

	ChunkSliceTimerFactory telemetry.FloatTimerFactory
}

func NewMetrics(meterProvider metric.MeterProvider) (Metrics, error) {
	var m Metrics

	blocksMeter := meterProvider.Meter("github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block/metrics")

	var err error
	if m.WriteChunksTimerFactory, err = telemetry.NewTimerFactory(
		blocksMeter, orchestratorBlockChunksStore,
		"Time taken to write memory chunks to disk",
		"Total bytes written to disk",
		"Total cache writes",
	); err != nil {
		return m, fmt.Errorf("failed to get stored chunks metric: %w", err)
	}

	if m.ChunkSliceTimerFactory, err = telemetry.NewFloatTimerFactory(
		blocksMeter, orchestratorChunkSlice,
		"Time taken by Chunker to serve a Slice() (source=mmap when served from cache)",
		"Bytes returned",
	); err != nil {
		return m, fmt.Errorf("error creating chunk slice timer factory: %w", err)
	}

	return m, nil
}
