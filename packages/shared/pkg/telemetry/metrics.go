package telemetry

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
)

type noopMetricExporter struct{}

func (noopMetricExporter) Temporality(sdkmetric.InstrumentKind) metricdata.Temporality {
	return metricdata.CumulativeTemporality
}

func (noopMetricExporter) Aggregation(sdkmetric.InstrumentKind) sdkmetric.Aggregation {
	return sdkmetric.AggregationDrop{}
}

func (noopMetricExporter) Export(context.Context, *metricdata.ResourceMetrics) error {
	return nil
}

func (noopMetricExporter) ForceFlush(context.Context) error {
	return nil
}

func (noopMetricExporter) Shutdown(context.Context) error {
	return nil
}

type meterExporterConfig struct {
	aggregationSelector sdkmetric.AggregationSelector
	temporalitySelector sdkmetric.TemporalitySelector
}

type MeterExporterOption func(*meterExporterConfig)

func WithMeterTemporalitySelector(selector sdkmetric.TemporalitySelector) MeterExporterOption {
	return func(config *meterExporterConfig) {
		config.temporalitySelector = selector
	}
}

func WithMeterAggregationSelector(selector sdkmetric.AggregationSelector) MeterExporterOption {
	return func(config *meterExporterConfig) {
		config.aggregationSelector = selector
	}
}

func NewMeterExporter(ctx context.Context, extraOptions ...MeterExporterOption) (sdkmetric.Exporter, error) {
	config := meterExporterConfig{}
	for _, option := range extraOptions {
		option(&config)
	}

	switch currentExportMode() {
	case exportModeCollectorGRPC:
		return newGRPCMeterExporter(ctx, config)
	case exportModeDirectHTTP:
		return newHTTPMeterExporter(ctx, config)
	default:
		return &noopMetricExporter{}, nil
	}
}

func newGRPCMeterExporter(ctx context.Context, config meterExporterConfig) (sdkmetric.Exporter, error) {
	opts := []otlpmetricgrpc.Option{}
	opts = append(opts,
		otlpmetricgrpc.WithInsecure(),
		otlpmetricgrpc.WithEndpoint(OTELCollectorGRPCEndpoint()),
	)
	if config.temporalitySelector != nil {
		opts = append(opts, otlpmetricgrpc.WithTemporalitySelector(config.temporalitySelector))
	}
	if config.aggregationSelector != nil {
		opts = append(opts, otlpmetricgrpc.WithAggregationSelector(config.aggregationSelector))
	}

	metricExporter, err := otlpmetricgrpc.New(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create metric exporter: %w", err)
	}

	return metricExporter, nil
}

func newHTTPMeterExporter(ctx context.Context, config meterExporterConfig) (sdkmetric.Exporter, error) {
	opts := []otlpmetrichttp.Option{}
	if config.temporalitySelector != nil {
		opts = append(opts, otlpmetrichttp.WithTemporalitySelector(config.temporalitySelector))
	}
	if config.aggregationSelector != nil {
		opts = append(opts, otlpmetrichttp.WithAggregationSelector(config.aggregationSelector))
	}

	metricExporter, err := otlpmetrichttp.New(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create metric exporter: %w", err)
	}

	return metricExporter, nil
}

func NewMeterProvider(metricsExporter sdkmetric.Exporter, metricExportPeriod time.Duration, res *resource.Resource, extraOption ...sdkmetric.Option) (metric.MeterProvider, error) {
	opts := []sdkmetric.Option{
		sdkmetric.WithReader(
			sdkmetric.NewPeriodicReader(
				metricsExporter,
				sdkmetric.WithInterval(metricExportPeriod),
			),
		),
	}

	if res != nil {
		opts = append(opts, sdkmetric.WithResource(res))
	}

	opts = append(opts, extraOption...)

	return sdkmetric.NewMeterProvider(opts...), nil
}
