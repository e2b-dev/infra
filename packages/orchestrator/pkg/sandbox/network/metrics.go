//go:build linux

package network

import (
	"context"
	"strconv"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

const datapathMetricName = "orchestrator.network.datapath"

// RegisterDatapathMetric reports the selected network datapath for this node.
// The caller owns the returned registration and must unregister it on shutdown.
func RegisterDatapathMetric(meterProvider metric.MeterProvider, networkVersion int) (metric.Registration, error) {
	meter := meterProvider.Meter("github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/network")
	gauge, err := meter.Int64ObservableGauge(
		datapathMetricName,
		metric.WithDescription("Selected orchestrator network datapath on this node."),
		metric.WithUnit("{node}"),
	)
	if err != nil {
		return nil, err
	}

	attrs := metric.WithAttributes(attribute.String("network_version", strconv.Itoa(networkVersion)))

	return meter.RegisterCallback(func(_ context.Context, observer metric.Observer) error {
		observer.ObserveInt64(gauge, 1, attrs)

		return nil
	}, gauge)
}
