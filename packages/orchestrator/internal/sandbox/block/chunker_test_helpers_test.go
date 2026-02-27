package block

import (
	"crypto/rand"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/metric/noop"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block/metrics"
	featureflags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

const (
	testBlockSize = header.PageSize // 4KB
)

func newTestMetrics(tb testing.TB) metrics.Metrics {
	tb.Helper()

	m, err := metrics.NewMetrics(noop.NewMeterProvider())
	require.NoError(tb, err)

	return m
}

func newTestFlags(t *testing.T) *featureflags.Client {
	t.Helper()

	flags, err := featureflags.NewClient()
	require.NoError(t, err)
	t.Cleanup(func() { _ = flags.Close(t.Context()) })

	return flags
}

func makeTestData(t *testing.T, size int) []byte {
	t.Helper()

	data := make([]byte, size)
	_, err := rand.Read(data)
	require.NoError(t, err)

	return data
}
