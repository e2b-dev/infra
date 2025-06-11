package metrics

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/exemplar"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.21.0"
)

var otelCollectorGRPCEndpoint = os.Getenv("OTEL_COLLECTOR_GRPC_ENDPOINT")

type MeterProvider struct {
	meterLock   sync.Mutex
	gaugesInt   map[GaugeIntType]metric.Int64ObservableGauge
	gaugesFloat map[GaugeFloatType]metric.Float64ObservableGauge

	meter         metric.Meter
	meterProvider *sdkmetric.MeterProvider
}

func NewSandboxMetricProvider(ctx context.Context, serviceVersion string, instanceID string, exportPeriod time.Duration) (*MeterProvider, error) {
	attributes := []attribute.KeyValue{
		semconv.ServiceName("e2b"),
		semconv.ServiceVersion(serviceVersion),
		semconv.ServiceInstanceID(instanceID),
		semconv.TelemetrySDKName("otel"),
		semconv.TelemetrySDKLanguageGo,
	}

	hostname, err := os.Hostname()
	if err == nil {
		attributes = append(attributes, semconv.HostName(hostname))
	}

	res, err := resource.New(
		ctx,
		resource.WithSchemaURL(semconv.SchemaURL),
		resource.WithAttributes(attributes...),
	)
	if err != nil {

		return nil, fmt.Errorf("failed to create resource: %w", err)
	}

	metricExporter, metricErr := otlpmetricgrpc.New(
		ctx,
		otlpmetricgrpc.WithInsecure(),
		otlpmetricgrpc.WithEndpoint(otelCollectorGRPCEndpoint),
		otlpmetricgrpc.WithTemporalitySelector(func(kind sdkmetric.InstrumentKind) metricdata.Temporality {
			// Use delta temporality for gauges and cumulative for all other instrument kinds.
			// This is used to prevent reporting sandbox metrics indefinitely.
			if kind == sdkmetric.InstrumentKindGauge {
				return metricdata.DeltaTemporality
			}
			return metricdata.CumulativeTemporality
		}),
	)
	if metricErr != nil {
		return nil, fmt.Errorf("failed to create metric exporter: %w", metricErr)
	}

	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithExemplarFilter(exemplar.AlwaysOffFilter),
		sdkmetric.WithReader(
			sdkmetric.NewPeriodicReader(
				metricExporter,
				sdkmetric.WithInterval(exportPeriod),
			),
		),
	)

	gaugesInt := make(map[GaugeIntType]metric.Int64ObservableGauge)
	gaugesFloat := make(map[GaugeFloatType]metric.Float64ObservableGauge)

	return &MeterProvider{
		meter:         mp.Meter("e2b"),
		meterProvider: mp,
		gaugesInt:     gaugesInt,
		gaugesFloat:   gaugesFloat,
	}, nil
}

func (mp *MeterProvider) Close(ctx context.Context) error {
	return mp.meterProvider.Shutdown(ctx)
}
