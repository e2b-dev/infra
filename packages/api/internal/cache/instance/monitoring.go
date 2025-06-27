package instance

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (c *InstanceCache) UpdateCounters(ctx context.Context, instance *InstanceInfo, value int64, newlyCreated bool) {
	attributes := []attribute.KeyValue{
		telemetry.WithTeamID(instance.TeamID.String()),
	}

	if value > 0 && newlyCreated {
		c.createdCounter.Add(ctx, value, metric.WithAttributes(attributes...))
	}

	c.sandboxCounter.Add(ctx, value, metric.WithAttributes(attributes...))
}
