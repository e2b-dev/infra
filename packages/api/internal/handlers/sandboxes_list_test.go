package handlers

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
)

// TestInstanceInfoToPaginatedSandboxes_PaginationTimestampPrecision guards the keyset
// pagination boundary: running sandboxes carry nanosecond StartTime while paused
// snapshots are microsecond-precision in Postgres. Only the PaginationTimestamp keyset
// value is truncated to microseconds (so the in-memory sort/cursor and the SQL predicate
// agree); the public StartedAt must keep its full precision so list responses match the
// sandbox detail endpoint.
func TestInstanceInfoToPaginatedSandboxes_PaginationTimestampPrecision(t *testing.T) {
	t.Parallel()

	// Sub-microsecond bits set (…789 ns) so truncation is observable.
	start := time.Date(2026, 1, 2, 3, 4, 5, 123456789, time.UTC)

	sandboxes := instanceInfoToPaginatedSandboxes([]sandbox.Sandbox{
		{SandboxID: "sbx", StartTime: start, State: sandbox.StateRunning},
	})

	require.Len(t, sandboxes, 1)

	assert.Equal(t, start, sandboxes[0].StartedAt, "public StartedAt must keep full precision")
	assert.Equal(t, start.Truncate(time.Microsecond), sandboxes[0].PaginationTimestamp,
		"pagination key must be microsecond-aligned")
	assert.Zero(t, sandboxes[0].PaginationTimestamp.Nanosecond()%1000,
		"pagination key should have no sub-microsecond bits")
}
