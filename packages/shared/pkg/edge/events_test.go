package edge

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseSandboxCatalogCreateEvent_WithEndTime(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 4, 7, 10, 0, 0, 0, time.UTC)
	end := start.Add(17 * time.Minute)

	md := SerializeSandboxCatalogCreateEvent(SandboxCatalogCreateEvent{
		SandboxID:               "sbx-1",
		ExecutionID:             "exec-1",
		OrchestratorID:          "orch-1",
		SandboxMaxLengthInHours: 2,
		SandboxStartTime:        start,
		SandboxEndTime:          end,
	})

	got, err := ParseSandboxCatalogCreateEvent(md)
	require.NoError(t, err)
	assert.Equal(t, end, got.SandboxEndTime)
}

func TestParseSandboxCatalogCreateEvent_WithoutEndTimeLeavesZeroValue(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 4, 7, 10, 0, 0, 0, time.UTC)

	md := SerializeSandboxCatalogCreateEvent(SandboxCatalogCreateEvent{
		SandboxID:               "sbx-1",
		ExecutionID:             "exec-1",
		OrchestratorID:          "orch-1",
		SandboxMaxLengthInHours: 2,
		SandboxStartTime:        start,
		SandboxEndTime:          start.Add(30 * time.Minute),
	})
	delete(md, sbxEndTimeHeader)

	got, err := ParseSandboxCatalogCreateEvent(md)
	require.NoError(t, err)
	assert.True(t, got.SandboxEndTime.IsZero())
}
