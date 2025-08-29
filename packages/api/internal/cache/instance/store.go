package instance

import (
	"context"
	"sync"

	cmap "github.com/orcaman/concurrent-map/v2"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

type MemoryStore struct {
	reservations *ReservationCache
	items        cmap.ConcurrentMap[string, *InstanceInfo]

	insertAnalytics func(ctx context.Context, data *InstanceInfo, created bool)
	deleteInstance  func(ctx context.Context, sbx *InstanceInfo, removeType RemoveType) error

	sandboxCounter metric.Int64UpDownCounter
	createdCounter metric.Int64Counter

	mu sync.Mutex
}

func NewStore(
	meterProvider metric.MeterProvider,
	insertAnalytics func(ctx context.Context, data *InstanceInfo, created bool),
	deleteInstance func(ctx context.Context, sbx *InstanceInfo, removeType RemoveType) error,
) *MemoryStore {
	// We will need to either use Redis or Consul's KV for storing active sandboxes to keep everything in sync,
	// right now we load them from Orchestrator
	meter := meterProvider.Meter("api.cache.sandbox")
	sandboxCounter, err := telemetry.GetUpDownCounter(meter, telemetry.SandboxCountMeterName)
	if err != nil {
		zap.L().Error("error getting counter", zap.Error(err))
	}

	createdCounter, err := telemetry.GetCounter(meter, telemetry.SandboxCreateMeterName)
	if err != nil {
		zap.L().Error("error getting counter", zap.Error(err))
	}

	instanceCache := &MemoryStore{
		items: cmap.New[*InstanceInfo](),

		insertAnalytics: insertAnalytics,
		deleteInstance:  deleteInstance,
		sandboxCounter:  sandboxCounter,
		createdCounter:  createdCounter,
		reservations:    NewReservationCache(),
	}

	return instanceCache
}
