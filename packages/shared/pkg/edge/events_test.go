package edge

import (
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"
)

func TestSandboxCatalogCreateEventRoundTrip(t *testing.T) {
	t.Parallel()

	event := SandboxCatalogCreateEvent{
		SandboxID:               "sbx",
		TeamID:                  "8f56d6bc-9b6d-4cbb-8e31-86b62359f716",
		ExecutionID:             "exec",
		OrchestratorID:          "orch",
		OrchestratorIP:          "10.0.0.7",
		SandboxMaxLengthInHours: 24,
		TrafficKeepalive:        true,
	}

	parsed, err := ParseSandboxCatalogCreateEvent(SerializeSandboxCatalogCreateEvent(event))
	require.NoError(t, err)
	require.Equal(t, &event, parsed)
}

func TestSandboxCatalogCreateEventParseAllowsMissingKeepaliveFields(t *testing.T) {
	t.Parallel()

	md := metadata.New(map[string]string{
		EventTypeHeader:               CatalogCreateEventType,
		SandboxIDHeader:               "sbx",
		SandboxExecutionIDHeader:      "exec",
		SandboxOrchestratorIDHeader:   "orch",
		SandboxMaxLengthInHoursHeader: "24",
	})

	parsed, err := ParseSandboxCatalogCreateEvent(md)
	require.NoError(t, err)
	require.Empty(t, parsed.TeamID)
	require.False(t, parsed.TrafficKeepalive)
}
