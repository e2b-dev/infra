package telemetry

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/log"
	noopLogs "go.opentelemetry.io/otel/log/noop"
	"go.opentelemetry.io/otel/metric"
	noopMetric "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/propagation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	noopTrace "go.opentelemetry.io/otel/trace/noop"
)

const metricExportPeriod = 15 * time.Second

type Client struct {
	MetricExporter  sdkmetric.Exporter
	MeterProvider   metric.MeterProvider
	SpanExporter    sdktrace.SpanExporter
	TracerProvider  trace.TracerProvider
	TracePropagator propagation.TextMapPropagator
	LogsExporter    sdklog.Exporter
	LogsProvider    log.LoggerProvider
}

func New(ctx context.Context, serviceName, commitSHA, clientID string) (*Client, error) {
	// Setup metrics
	metricsExporter, err := NewMeterExporter(ctx, otlpmetricgrpc.WithAggregationSelector(func(kind sdkmetric.InstrumentKind) sdkmetric.Aggregation {
		if kind == sdkmetric.InstrumentKindHistogram {
			// Defaults from https://github.com/open-telemetry/opentelemetry-specification/blob/main/specification/metrics/sdk.md#base2-exponential-bucket-histogram-aggregation
			return sdkmetric.AggregationBase2ExponentialHistogram{
				MaxSize:  160,
				MaxScale: 20,
				NoMinMax: false,
			}
		}
		return sdkmetric.DefaultAggregationSelector(kind)
	}))
	if err != nil {
		return nil, fmt.Errorf("failed to create metrics exporter: %w", err)
	}

	meterProvider, err := NewMeterProvider(ctx, metricsExporter, metricExportPeriod, serviceName, commitSHA, clientID)
	if err != nil {
		return nil, fmt.Errorf("failed to create metrics provider: %w", err)
	}

	// Setup logging
	logsExporter, err := NewLogExporter(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create logs exporter: %w", err)
	}

	logsProvider, err := NewLogProvider(ctx, logsExporter, serviceName, commitSHA, clientID)
	if err != nil {
		return nil, fmt.Errorf("failed to create logs provider: %w", err)
	}

	// Setup tracing
	spanExporter, err := NewSpanExporter(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create span exporter: %w", err)
	}

	tracerProvider, err := NewTracerProvider(ctx, spanExporter, serviceName, commitSHA, clientID)
	if err != nil {
		return nil, fmt.Errorf("failed to create tracer provider: %w", err)
	}

	// There's probably not a reason why not to set the trace propagator globally, it's used in SDKs
	propagator := NewTextPropagator()
	otel.SetTextMapPropagator(propagator)

	return &Client{
		MetricExporter:  metricsExporter,
		MeterProvider:   meterProvider,
		SpanExporter:    spanExporter,
		TracerProvider:  tracerProvider,
		TracePropagator: propagator,
		LogsExporter:    logsExporter,
		LogsProvider:    logsProvider,
	}, nil
}

func (t *Client) Shutdown(ctx context.Context) error {
	var errs []error
	if t.MetricExporter != nil {
		if err := t.MetricExporter.Shutdown(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	if t.SpanExporter != nil {
		if err := t.SpanExporter.Shutdown(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	if t.LogsExporter != nil {
		if err := t.LogsExporter.Shutdown(ctx); err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

func NewNoopClient() *Client {
	return &Client{
		MetricExporter:  &noopMetricExporter{},
		MeterProvider:   noopMetric.MeterProvider{},
		SpanExporter:    &noopSpanExporter{},
		TracerProvider:  noopTrace.NewTracerProvider(),
		TracePropagator: propagation.NewCompositeTextMapPropagator(),
		LogsExporter:    &noopLogExporter{},
		LogsProvider:    noopLogs.NewLoggerProvider(),
	}
}
