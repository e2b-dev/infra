package telemetry

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/metric/noop"
)

// allEnvdDefaultsCounters is every counter this feature emits. Listed once, so adding a
// counter without a description or unit fails the test below by name rather than shipping an
// unlabelled metric — which is the omission this test exists for, and one it has already
// missed once by enumerating the counters inline.
var allEnvdDefaultsCounters = []CounterType{
	EnvdDefaultsApplied,
	EnvdDefaultsMismatch,
	EnvdDefaultsWorkdirWithheld,
	EnvdDefaultsBuiltinFallback,
}

// TestEnvdDefaultsMetricsRegistered guards the defaults signals: each needs a description
// and a unit entry (a missing one silently ships an unlabelled metric) and must construct.
//
// These counters are the only evidence that a sandbox's default user is what the host
// intended, so an unlabelled or unconstructable counter is not a cosmetic problem — it is
// the signal the rollout gate reads.
func TestEnvdDefaultsMetricsRegistered(t *testing.T) {
	t.Parallel()

	for _, c := range allEnvdDefaultsCounters {
		assert.NotEmptyf(t, counterDesc[c], "missing description for counter %s", c)
		assert.NotEmptyf(t, counterUnits[c], "missing unit for counter %s", c)
	}

	m := noop.NewMeterProvider().Meter("github.com/e2b-dev/infra/packages/shared/pkg/telemetry")
	for _, c := range allEnvdDefaultsCounters {
		_, err := GetCounter(m, c)
		require.NoErrorf(t, err, "counter %s failed to construct", c)
	}
}
