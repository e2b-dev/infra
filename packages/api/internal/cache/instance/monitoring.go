package instance

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

func (c *InstanceCache) UpdateCounter(instance InstanceInfo, value int64) {
	if c.counter == nil {
		return
	}

	counter := *c.counter
	attributes := []attribute.KeyValue{
		attribute.String("env_id", instance.Instance.TemplateID),
		attribute.String("team_id", instance.TeamID.String()),
	}

	counter.Add(context.Background(), value, metric.WithAttributes(attributes...))
}
