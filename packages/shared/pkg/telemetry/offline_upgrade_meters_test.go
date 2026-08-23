package telemetry

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/metric/noop"
)

// A metric shipped without a description/unit map entry silently emits an
// undocumented series. Guard the offline-upgrade counter and duration histogram.
func TestOfflineUpgradeMetersPopulated(t *testing.T) {
	t.Parallel()

	assert.NotEmptyf(t, counterDesc[OrchestratorEnvdOfflineUpgradeAttempts], "missing description for counter %s", OrchestratorEnvdOfflineUpgradeAttempts)
	assert.NotEmptyf(t, counterUnits[OrchestratorEnvdOfflineUpgradeAttempts], "missing unit for counter %s", OrchestratorEnvdOfflineUpgradeAttempts)
	assert.NotEmptyf(t, histogramDesc[OrchestratorEnvdOfflineUpgradeDurationName], "missing description for histogram %s", OrchestratorEnvdOfflineUpgradeDurationName)
	assert.NotEmptyf(t, histogramUnits[OrchestratorEnvdOfflineUpgradeDurationName], "missing unit for histogram %s", OrchestratorEnvdOfflineUpgradeDurationName)

	meter := noop.NewMeterProvider().Meter("github.com/e2b-dev/infra/packages/shared/pkg/telemetry")
	_, err := GetCounter(meter, OrchestratorEnvdOfflineUpgradeAttempts)
	require.NoError(t, err)
	_, err = GetHistogram(meter, OrchestratorEnvdOfflineUpgradeDurationName)
	require.NoError(t, err)
}
