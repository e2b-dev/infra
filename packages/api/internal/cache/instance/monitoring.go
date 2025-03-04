package instance

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

func (c *InstanceCache) UpdateCounters(instance *InstanceInfo, value int64, newlyCreated bool) {
	attributes := []attribute.KeyValue{
		attribute.String("team_id", instance.TeamID.String()),
	}

	if value > 0 && newlyCreated {
		c.createdCounter.Add(context.Background(), value, metric.WithAttributes(attributes...))
	}

	c.sandboxCounter.Add(context.Background(), value, metric.WithAttributes(attributes...))
}
