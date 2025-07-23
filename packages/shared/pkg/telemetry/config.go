package telemetry

import (
	"context"
	"fmt"
	"os"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/semconv/v1.21.0"
)

var OtelCollectorGRPCEndpoint = os.Getenv("OTEL_COLLECTOR_GRPC_ENDPOINT")

func getResource(ctx context.Context, serviceName, serviceVersion, instanceID string) (*resource.Resource, error) {
	attributes := []attribute.KeyValue{
		semconv.ServiceName(serviceName),
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

	return res, nil
}
