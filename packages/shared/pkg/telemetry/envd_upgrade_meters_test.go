package telemetry

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/metric/noop"
)

// TestEnvdUpgradeMetricsRegistered guards the rollout metrics: each must have a
// description and unit map entry (an easy omission — a missing entry silently
// ships an unlabelled metric) and must construct without error.
//
// The binary-cache series belong here rather than beside the cache: they are
// declared in this package and read on the same dashboard as the upgrade itself,
// and a name added to the enum with no map entry is exactly the omission this
// test exists to catch.
func TestEnvdUpgradeMetricsRegistered(t *testing.T) {
	t.Parallel()

	m := noop.NewMeterProvider().Meter("github.com/e2b-dev/infra/packages/shared/pkg/telemetry")

	for _, c := range []CounterType{
		OrchestratorEnvdUpgradeAttempts,
		OrchestratorEnvdUpgradeGated,
		OrchestratorEnvdUpgradeHandover,
		OrchestratorEnvdBinaryCacheReads,
		OrchestratorEnvdBinaryCacheDeliveries,
		OrchestratorEnvdBinaryCacheWarms,
	} {
		assert.NotEmptyf(t, counterDesc[c], "missing description for counter %s", c)
		assert.NotEmptyf(t, counterUnits[c], "missing unit for counter %s", c)

		_, err := GetCounter(m, c)
		require.NoErrorf(t, err, "counter %s does not construct", c)
	}

	for _, h := range []HistogramType{
		OrchestratorEnvdUpgradeDurationName,
		OrchestratorEnvdUpgradePhaseDurationName,
	} {
		assert.NotEmptyf(t, histogramDesc[h], "missing description for histogram %s", h)
		assert.NotEmptyf(t, histogramUnits[h], "missing unit for histogram %s", h)

		_, err := GetHistogram(m, h)
		require.NoErrorf(t, err, "histogram %s does not construct", h)
	}
}
