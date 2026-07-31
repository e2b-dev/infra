package telemetry

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/metric/noop"
)

// A metric shipped without a description/unit map entry is an easy omission that
// silently emits an undocumented series. Guard the fs_quiesced counter.
func TestFsQuiescedCounterMeterMapsPopulated(t *testing.T) {
	t.Parallel()

	assert.NotEmptyf(t, counterDesc[SandboxPauseFsQuiescedCounterName], "missing description for counter %s", SandboxPauseFsQuiescedCounterName)
	assert.NotEmptyf(t, counterUnits[SandboxPauseFsQuiescedCounterName], "missing unit for counter %s", SandboxPauseFsQuiescedCounterName)

	meter := noop.NewMeterProvider().Meter("github.com/e2b-dev/infra/packages/shared/pkg/telemetry")
	_, err := GetCounter(meter, SandboxPauseFsQuiescedCounterName)
	require.NoError(t, err)
}
