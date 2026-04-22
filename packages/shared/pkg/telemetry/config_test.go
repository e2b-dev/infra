package telemetry

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestOTELCollectorGRPCEndpoint(t *testing.T) {
	t.Setenv(otelCollectorGRPCEndpointEnv, " localhost:4317 ")

	assert.Equal(t, "localhost:4317", OTELCollectorGRPCEndpoint())
	assert.False(t, OTLPHTTPEnabled())
}

func TestOTLPHTTPEnabled(t *testing.T) {
	t.Setenv(otelExporterOTLPEndpointEnv, " https://grafana.example.com/otlp ")

	assert.Equal(t, "https://grafana.example.com/otlp", OTLPHTTPEndpoint())
	assert.True(t, OTLPHTTPEnabled())
	assert.Empty(t, OTELCollectorGRPCEndpoint())
}
