//go:build linux

package factories

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/cfg"
)

// clickhouse.NewDriver does not dial, so this needs no server.

func TestOpenClickhouseEndpointsWithoutConfiguration(t *testing.T) {
	t.Parallel()

	endpoints, closers := openClickhouseEndpoints(t.Context(), cfg.Config{})

	assert.Empty(t, endpoints)
	assert.Empty(t, closers)
}

func TestOpenClickhouseEndpointsMarksTheSingularStringPrimary(t *testing.T) {
	t.Parallel()

	endpoints, closers := openClickhouseEndpoints(t.Context(), cfg.Config{
		ClickhouseConnectionString: "clickhouse://user:secret@primary.example:9000/db",
	})

	require.Len(t, endpoints, 1)
	assert.True(t, endpoints[0].Primary)
	assert.NotNil(t, endpoints[0].Conn)
	assert.Empty(t, endpoints[0].Label)
	assert.Equal(t, "sandbox-events", endpoints[0].BatcherName("sandbox-events"))

	require.Len(t, closers, 1)
	closeEndpoints(t, closers)
}

func TestOpenClickhouseEndpointsLabelsAdditionalOnesWithoutCredentials(t *testing.T) {
	t.Parallel()

	endpoints, closers := openClickhouseEndpoints(t.Context(), cfg.Config{
		ClickhouseConnectionString: "clickhouse://user:secret@primary.example:9000/db",
		ClickhouseConnectionStrings: []string{
			"clickhouse://user:secret@primary.example:9000/db", // duplicate of the primary
			"clickhouse://user:secret@second.example:9000/db",
		},
	})

	require.Len(t, endpoints, 2, "the duplicate of the primary must not be opened twice")

	additional := endpoints[1]
	assert.False(t, additional.Primary)
	assert.Equal(t, "second.example:9000", additional.Label, "the label must be host:port, never the DSN")
	assert.NotContains(t, additional.Label, "secret")
	assert.Equal(t, "sandbox-events:second.example:9000", additional.BatcherName("sandbox-events"))

	// The primary is registered first, so the reverse order releases it last.
	require.Len(t, closers, 2)
	assert.Equal(t, "clickhouse connection", closers[0].name)
	assert.Equal(t, "clickhouse connection second.example:9000", closers[1].name)
	closeEndpoints(t, closers)
}

func TestOpenClickhouseEndpointsSkipsAnUnusableAdditionalEndpoint(t *testing.T) {
	t.Parallel()

	endpoints, closers := openClickhouseEndpoints(t.Context(), cfg.Config{
		ClickhouseConnectionStrings: []string{
			"://not-a-dsn",
			"clickhouse://user:secret@second.example:9000/db",
		},
	})

	require.Len(t, endpoints, 1, "an unparseable additional endpoint is skipped, not fatal")
	assert.Equal(t, "second.example:9000", endpoints[0].Label)
	closeEndpoints(t, closers)
}

func closeEndpoints(t *testing.T, closers []closer) {
	t.Helper()

	for _, c := range closers {
		assert.NoError(t, c.close(t.Context()))
	}
}
