//go:build linux

package server

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/envdbin"
)

// The delivery phase has three outcomes, not two. CallEnvdUpgrade deliberately
// returns a nil error when its deadline fired or the parent was cancelled --
// envd may still be mid-handover, so the caller must keep a follow-up not-ready
// recoverable. Labelling that "success" would hide the one stall this phase was
// added to expose.
func TestDeliveryResultDistinguishesAnUnconfirmedDelivery(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name          string
		execConfirmed bool
		err           error
		want          string
	}{
		{name: "confirmed exec", execConfirmed: true, want: "success"},
		{name: "delivery error", err: errors.New("connection refused"), want: "failed"},
		{
			// The 30 s budget fired: nil error, exec not confirmed. This is the case
			// the phase histogram exists for.
			name: "deadline with no confirmed exec", execConfirmed: false, want: "unconfirmed",
		},
		{
			// An error wins over the confirmation flag: nothing was delivered.
			name: "error even with the flag set", execConfirmed: true,
			err: errors.New("dial"), want: "failed",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tc.want, deliveryResult(tc.execConfirmed, tc.err))
		})
	}
}

func TestPhaseResultIsBinary(t *testing.T) {
	t.Parallel()

	require.Equal(t, "success", phaseResult(nil))
	require.Equal(t, "failed", phaseResult(errors.New("boom")))
}

// TestResolvePhaseResult pins which resolutions belong in the resolve histogram.
//
// The histogram exists to expose the version probe — the dominant cost of a cold
// upgrade, and absent from the combined duration histogram because it runs before
// that starts timing. Recording the reasons that return before getVersion is called
// fills the series with samples that timed nothing; since those are also the most
// frequent, they would dominate it and hide the cost it was added to show.
func TestResolvePhaseResult(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name         string
		path         string
		reason       string
		wantResult   string
		wantRecorded bool
	}{
		{"an upgrade resolved", "/fc-envd/envd", "", "success", true},
		// These return before getVersion: nothing was probed, so nothing is timed.
		{"flag off", "", "off", "", false},
		{"target not staged", "", "not_staged", "", false},
		{"invalid target", "", "invalid_target", "", false},
		// These paid for the probe in full, and same_version is the majority.
		{"already on the target", "", "same_version", "same_version", true},
		{"downgrade refused", "", "downgrade", "downgrade", true},
		{"probe failed", "", "getversion_failed", "getversion_failed", true},
		// This consulted the cache and deliberately did not probe — worth timing,
		// because it is what a hit-gated resolve costs.
		{"deferred, not cached", "", envdbin.ReasonNotCached, envdbin.ReasonNotCached, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			result, recorded := resolvePhaseResult(tc.path, tc.reason)
			require.Equal(t, tc.wantRecorded, recorded, "recorded")
			require.Equal(t, tc.wantResult, result, "result")
		})
	}
}
