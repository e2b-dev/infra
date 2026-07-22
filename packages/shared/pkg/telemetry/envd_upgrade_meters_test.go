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
func TestEnvdUpgradeMetricsRegistered(t *testing.T) {
	t.Parallel()

	for _, c := range []CounterType{OrchestratorEnvdUpgradeAttempts, OrchestratorEnvdUpgradeGated, OrchestratorEnvdUpgradeHandover} {
		assert.NotEmptyf(t, counterDesc[c], "missing description for counter %s", c)
		assert.NotEmptyf(t, counterUnits[c], "missing unit for counter %s", c)
	}
	assert.NotEmpty(t, histogramDesc[OrchestratorEnvdUpgradeDurationName], "missing histogram description")
	assert.NotEmpty(t, histogramUnits[OrchestratorEnvdUpgradeDurationName], "missing histogram unit")

	m := noop.NewMeterProvider().Meter("github.com/e2b-dev/infra/packages/shared/pkg/telemetry")
	_, err := GetCounter(m, OrchestratorEnvdUpgradeAttempts)
	require.NoError(t, err)
	_, err = GetCounter(m, OrchestratorEnvdUpgradeGated)
	require.NoError(t, err)
	_, err = GetCounter(m, OrchestratorEnvdUpgradeHandover)
	require.NoError(t, err)
	_, err = GetHistogram(m, OrchestratorEnvdUpgradeDurationName)
	require.NoError(t, err)
}
