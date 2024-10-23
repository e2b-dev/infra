package instance

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/e2b-dev/infra/packages/api/internal/meters"
)

func (c *InstanceCache) UpdateCounter(instance InstanceInfo, value int64, sync bool) {
	attributes := []attribute.KeyValue{
		attribute.String("team_id", instance.TeamID.String()),
	}

	if value > 0 && !sync {
		createdCounter, err := meters.GetCounter(instanceCreateMeterName)
		if err != nil {
			c.logger.Errorw("error getting counter", "error", err)
			return
		} else {
			createdCounter.Add(context.Background(), value, metric.WithAttributes(attributes...))
		}
	}

	instanceCountCounter, err := meters.GetUpDownCounter(instanceCountMeterName)
	if err != nil {
		c.logger.Errorw("error getting counter", "error", err)
		return
	}

	instanceCountCounter.Add(context.Background(), value, metric.WithAttributes(attributes...))
}
