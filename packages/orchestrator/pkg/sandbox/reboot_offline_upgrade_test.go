//go:build linux

package sandbox

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestDecideOfflineSwap pins the cold-boot envd-swap gate: it must swap only when
// the resolver returns a target AND the snapshot's rootfs was frozen at pause, and
// otherwise report the no-op correctly. The frozen (fs_quiesced) gate is the
// load-bearing safety property — a torn-journal rootfs must never be rewritten.
func TestDecideOfflineSwap(t *testing.T) {
	t.Parallel()

	const target = "/fc-envd/envd.abc123"

	tests := []struct {
		name            string
		resolverPath    string
		reason          string
		fsQuiesced      bool
		wantSwap        bool
		wantCountResult string // offline_upgrade.attempts{result=...} for a gated no-op ("" = none)
		wantLog         bool   // log the misconfigured/unstaged reason
	}{
		// Flag off — the only no-op that stays invisible, because the
		// fs_only cold-boot population is already counted elsewhere.
		{"off, quiesced", "", "off", true, false, "", false},
		{"empty reason, quiesced", "", "", true, false, "", false},
		// Already on the target: counted, not logged. This is the population the
		// rollout is trying to grow, so it must be visible; it is also the expected
		// steady state, so logging it per resume would be noise.
		{"same_version, quiesced", "", "same_version", true, false, "same_version", false},
		// No upgrade from the resolver — misconfigured target, counted AND logged:
		// counted so a ramp sees the size of the excluded population, logged because
		// each one is an operator error rather than an expected outcome.
		{"not_staged", "", "not_staged", true, false, "not_staged", true},
		{"downgrade refused", "", "downgrade", true, false, "downgrade", true},
		{"invalid_target", "", "invalid_target", true, false, "invalid_target", true},
		{"getversion_failed", "", "getversion_failed", true, false, "getversion_failed", true},
		// An unrecognised reason must not vanish: the resolver's vocabulary can grow,
		// and a new reason silently counted as nothing is a hole in the rollout view.
		{"unknown future reason", "", "unresolvable_frobnicator", true, false, "unresolvable_frobnicator", true},
		// Upgrade wanted, but the rootfs is NOT crash-consistent → gated skip, counted.
		{"target but not frozen", target, "", false, false, "not_quiesced", false},
		// The one path that actually swaps: upgrade wanted AND rootfs frozen.
		{"target and frozen", target, "", true, true, "", false},
		// A benign resolver no-op is never overridden into a swap by fs_quiesced.
		{"off wins over frozen", "", "off", true, false, "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := decideOfflineSwap(tt.resolverPath, tt.reason, tt.fsQuiesced)
			assert.Equal(t, tt.wantSwap, got.swap, "swap")
			assert.Equal(t, tt.wantCountResult, got.countResult, "countResult")
			assert.Equal(t, tt.wantLog, got.logMisconfig, "logMisconfig")
			// A gated no-op is never also a swap, and a swap never carries a no-op count.
			if got.swap {
				assert.Empty(t, got.countResult, "a swap must not also count a no-op")
				assert.False(t, got.logMisconfig, "a swap must not log a misconfig")
			}
			// Every no-op the resolver named is counted, and counted AS that reason —
			// so `sum by (result)` reads back the resolver's own vocabulary instead of a
			// bucket someone has to decode. `off` is the sole exemption.
			if tt.reason != "" && tt.reason != "off" {
				assert.Equal(t, tt.reason, got.countResult,
					"a named resolver no-op must be counted under its own reason")
			}
		})
	}
}
