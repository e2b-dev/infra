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
		// No upgrade from the resolver — benign no-ops, silent, no metric.
		{"off, quiesced", "", "off", true, false, "", false},
		{"empty reason, quiesced", "", "", true, false, "", false},
		{"same_version, quiesced", "", "same_version", true, false, "", false},
		// No upgrade from the resolver — misconfigured target, logged, no metric.
		{"not_staged", "", "not_staged", true, false, "", true},
		{"downgrade refused", "", "downgrade", true, false, "", true},
		{"invalid_target", "", "invalid_target", true, false, "", true},
		{"getversion_failed", "", "getversion_failed", true, false, "", true},
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
		})
	}
}
