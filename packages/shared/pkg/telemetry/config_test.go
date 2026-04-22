package telemetry

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestOTELCollectorGRPCEndpoint(t *testing.T) {
	t.Setenv(otelExporterOTLPEndpointEnv, "")
	t.Setenv(otelExporterOTLPTracesEndpointEnv, "")
	t.Setenv(otelExporterOTLPMetricsEndpointEnv, "")
	t.Setenv(otelExporterOTLPLogsEndpointEnv, "")
	t.Setenv(otelCollectorGRPCEndpointEnv, " localhost:4317 ")

	assert.Equal(t, "localhost:4317", OTELCollectorGRPCEndpoint())
	assert.False(t, OTLPHTTPEnabled())
}

func TestOTLPHTTPEnabled(t *testing.T) {
	t.Setenv(otelCollectorGRPCEndpointEnv, "")
	t.Setenv(otelExporterOTLPEndpointEnv, " https://grafana.example.com/otlp ")

	assert.Equal(t, "https://grafana.example.com/otlp", OTLPHTTPEndpoint())
	assert.True(t, OTLPHTTPEnabled())
	assert.Empty(t, OTELCollectorGRPCEndpoint())
}

func TestOTLPHTTPEnabledWithSignalSpecificEndpoint(t *testing.T) {
	t.Setenv(otelCollectorGRPCEndpointEnv, "")
	t.Setenv(otelExporterOTLPEndpointEnv, "")
	t.Setenv(otelExporterOTLPTracesEndpointEnv, "https://grafana.example.com/v1/traces")

	assert.True(t, OTLPHTTPEnabled())
}
