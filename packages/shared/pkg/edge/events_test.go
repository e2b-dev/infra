package edge

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"

	catalog "github.com/e2b-dev/infra/packages/shared/pkg/sandbox-catalog"
)

func TestSandboxCatalogCreateEventRoundTrip(t *testing.T) {
	t.Parallel()

	startTime := time.Date(2026, 4, 20, 12, 0, 0, 0, time.UTC)
	event := SandboxCatalogCreateEvent{
		SandboxID:               "sbx",
		TeamID:                  "8f56d6bc-9b6d-4cbb-8e31-86b62359f716",
		ExecutionID:             "exec",
		OrchestratorID:          "orch",
		OrchestratorIP:          "10.0.0.7",
		SandboxMaxLengthInHours: 24,
		SandboxStartTime:        startTime,
		Keepalive: &catalog.Keepalive{
			Traffic: &catalog.TrafficKeepalive{
				Enabled: true,
			},
		},
	}

	parsed, err := ParseSandboxCatalogCreateEvent(SerializeSandboxCatalogCreateEvent(event))
	require.NoError(t, err)
	require.Equal(t, &event, parsed)
}

func TestSandboxCatalogCreateEventParseAllowsMissingKeepaliveFields(t *testing.T) {
	t.Parallel()

	startTime := time.Date(2026, 4, 20, 12, 0, 0, 0, time.UTC)
	md := metadata.New(map[string]string{
		EventTypeHeader:           CatalogCreateEventType,
		sbxIdHeader:               "sbx",
		sbxExecutionIdHeader:      "exec",
		sbxOrchestratorIdHeader:   "orch",
		sbxMaxLengthInHoursHeader: "24",
		sbxStartTimeHeader:        startTime.Format(time.RFC3339),
	})

	parsed, err := ParseSandboxCatalogCreateEvent(md)
	require.NoError(t, err)
	require.Equal(t, startTime, parsed.SandboxStartTime)
	require.Empty(t, parsed.TeamID)
	require.Nil(t, parsed.Keepalive)
}
