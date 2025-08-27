package instance

import (
	"context"
	"sync"

	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

type InstanceCache struct {
	reservations *ReservationCache

	cache          *lifecycleCache[*InstanceInfo]
	insertInstance func(data *InstanceInfo, created bool) error

	sandboxCounter metric.Int64UpDownCounter
	createdCounter metric.Int64Counter

	mu sync.Mutex
}

func NewCache(
	ctx context.Context,
	meterProvider metric.MeterProvider,
	insertInstance func(data *InstanceInfo, created bool) error,
	deleteInstance func(data *InstanceInfo) error,
) *InstanceCache {
	// We will need to either use Redis or Consul's KV for storing active sandboxes to keep everything in sync,
	// right now we load them from Orchestrator
	cache := newLifecycleCache[*InstanceInfo]()

	meter := meterProvider.Meter("api.cache.sandbox")
	sandboxCounter, err := telemetry.GetUpDownCounter(meter, telemetry.SandboxCountMeterName)
	if err != nil {
		zap.L().Error("error getting counter", zap.Error(err))
	}

	createdCounter, err := telemetry.GetCounter(meter, telemetry.SandboxCreateMeterName)
	if err != nil {
		zap.L().Error("error getting counter", zap.Error(err))
	}

	instanceCache := &InstanceCache{
		cache:          cache,
		insertInstance: insertInstance,
		sandboxCounter: sandboxCounter,
		createdCounter: createdCounter,
		reservations:   NewReservationCache(),
	}

	cache.OnEviction(func(ctx context.Context, instanceInfo *InstanceInfo) {
		err := deleteInstance(instanceInfo)
		if err != nil {
			zap.L().Error("Error deleting instance", zap.Error(err))
		}

		instanceCache.UpdateCounters(ctx, instanceInfo, -1, false)
	})

	go cache.Start(ctx)

	return instanceCache
}
