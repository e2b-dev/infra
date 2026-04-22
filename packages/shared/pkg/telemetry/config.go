package telemetry

import (
	"context"
	"fmt"
	"os"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.21.0"
)

const (
	otelCollectorGRPCEndpointEnv = "OTEL_COLLECTOR_GRPC_ENDPOINT"
	otelExporterOTLPEndpointEnv  = "OTEL_EXPORTER_OTLP_ENDPOINT"
)

func OTELCollectorGRPCEndpoint() string {
	return strings.TrimSpace(os.Getenv(otelCollectorGRPCEndpointEnv))
}

func OTLPHTTPEndpoint() string {
	return strings.TrimSpace(os.Getenv(otelExporterOTLPEndpointEnv))
}

func OTLPHTTPEnabled() bool {
	return OTLPHTTPEndpoint() != ""
}

func GetResource(ctx context.Context, nodeID, serviceName, serviceCommit, serviceVersion, serviceInstanceID string, additional ...attribute.KeyValue) (*resource.Resource, error) {
	attributes := []attribute.KeyValue{
		semconv.ServiceName(serviceName),
		semconv.ServiceVersion(fmt.Sprintf("%s-%s", serviceVersion, serviceCommit)),
		semconv.ServiceInstanceID(serviceInstanceID),
		semconv.TelemetrySDKName("otel"),
		semconv.HostID(nodeID),
		semconv.TelemetrySDKLanguageGo,
	}

	attributes = append(attributes, additional...)
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
