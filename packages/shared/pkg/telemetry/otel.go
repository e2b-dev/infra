package telemetry

import (
	"context"
	"fmt"
	baselog "log"
	"os"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/log/global"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.21.0"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/encoding/gzip"
)

const (
	metricExportPeriod = 15 * time.Second
)

var otelCollectorGRPCEndpoint = os.Getenv("OTEL_COLLECTOR_GRPC_ENDPOINT")

type client struct {
	tracerProvider *sdktrace.TracerProvider
	meterProvider  *metric.MeterProvider
	logsProvider   *log.LoggerProvider
}

// InitOTLPExporter initializes an OTLP exporter, and configures the corresponding trace providers.
func InitOTLPExporter(ctx context.Context, serviceName, serviceVersion string) func(ctx context.Context) error {
	attributes := []attribute.KeyValue{
		semconv.ServiceName(serviceName),
		semconv.ServiceVersion(serviceVersion),
		semconv.TelemetrySDKName("otel"),
		semconv.ServiceInstanceID(uuid.New().String()),
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
		panic(fmt.Errorf("failed to create resource: %w", err))
	}

	var otelClient client

	go func() {
		// Set up a connection to the collector.
		var conn *grpc.ClientConn

		retryInterval := 5 * time.Second

		for {
			dialCtx, cancel := context.WithTimeout(ctx, time.Second)

			conn, err = grpc.DialContext(dialCtx,
				otelCollectorGRPCEndpoint,
				// Note the use of insecure transport here. TLS is recommended in production.
				grpc.WithTransportCredentials(insecure.NewCredentials()),
				grpc.WithBlock(),
			)

			cancel()

			if err != nil {
				baselog.Printf("Failed to connect to otel collector, not using OTEL for logs: %v", err)
				time.Sleep(retryInterval)
			} else {
				break
			}
		}

		// Set up a trace exporter
		traceExporter, traceErr := otlptracegrpc.New(
			ctx,
			otlptracegrpc.WithGRPCConn(conn),
			otlptracegrpc.WithCompressor(gzip.Name),
		)
		if traceErr != nil {
			panic(fmt.Errorf("failed to create trace exporter: %w", err))
		}

		// Register the trace exporter with a TracerProvider, using a batch
		// span processor to aggregate spans before export.
		bsp := sdktrace.NewBatchSpanProcessor(traceExporter)
		tracerProvider := sdktrace.NewTracerProvider(
			sdktrace.WithSampler(sdktrace.AlwaysSample()),
			sdktrace.WithResource(res),
			sdktrace.WithSpanProcessor(bsp),
		)

		otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
		otel.SetTracerProvider(tracerProvider)
		otelClient.tracerProvider = tracerProvider

		metricExporter, metricErr := otlpmetricgrpc.New(ctx, otlpmetricgrpc.WithGRPCConn(conn))
		if metricErr != nil {
			panic(fmt.Errorf("failed to create metric exporter: %w", err))
		}

		meterProvider := metric.NewMeterProvider(
			metric.WithResource(res),
			metric.WithReader(
				metric.NewPeriodicReader(
					metricExporter,
					metric.WithInterval(metricExportPeriod),
				),
			),
		)

		otel.SetMeterProvider(meterProvider)
		otelClient.meterProvider = meterProvider

		logsExporter, logsErr := otlploggrpc.New(
			ctx,
			otlploggrpc.WithGRPCConn(conn),
			otlploggrpc.WithCompressor(gzip.Name),
		)
		if logsErr != nil {
			panic(fmt.Errorf("failed to create logs exporter: %w", err))
		}

		logsProcessor := log.NewBatchProcessor(logsExporter)
		logsProvider := log.NewLoggerProvider(
			log.WithResource(res),
			log.WithProcessor(logsProcessor),
		)

		global.SetLoggerProvider(logsProvider)
		otelClient.logsProvider = logsProvider
	}()

	// Shutdown will flush any remaining spans and shut down the exporter.
	return otelClient.close
}

func (c *client) close(ctx context.Context) error {
	if c.tracerProvider != nil {
		if err := c.tracerProvider.Shutdown(ctx); err != nil {
			return err
		}
	}

	if c.meterProvider != nil {
		if err := c.meterProvider.Shutdown(ctx); err != nil {
			return err
		}
	}

	if c.logsProvider != nil {
		if err := c.logsProvider.Shutdown(ctx); err != nil {
			return err
		}
	}

	return nil
}
