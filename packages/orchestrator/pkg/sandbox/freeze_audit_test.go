//go:build linux

package sandbox

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFreezeAudit_DecodesEnvdsHeader is the reading half of the X-Envd-Freeze-Audit contract; the
// writing half is pinned in envd's own test against the same literal. The two components ship
// independently, so both directions of version skew have to decode without a lockstep release:
// a field envd adds must not fail the parse here, and one it has not learned to send yet must
// read as the safe value.
func TestFreezeAudit_DecodesEnvdsHeader(t *testing.T) {
	t.Parallel()

	t.Run("the header envd sends today", func(t *testing.T) {
		t.Parallel()

		var a freezeAudit
		require.NoError(t, json.Unmarshal(
			[]byte(`{"visited":12,"frozen":5,"escaped":1,"violations":0,"truncated":true}`), &a))
		assert.Equal(t, freezeAudit{Visited: 12, Frozen: 5, Escaped: 1, Truncated: true}, a)
	})

	// Forward skew: a newer envd during a rollout. Dropping the whole audit over a field this
	// orchestrator does not know about would silence the signal on exactly the fleet that is
	// mid-upgrade -- the one most likely to have something to report.
	t.Run("a field this orchestrator does not know is ignored", func(t *testing.T) {
		t.Parallel()

		var a freezeAudit
		require.NoError(t, json.Unmarshal(
			[]byte(`{"visited":3,"violations":2,"allowlisted":7}`), &a))
		assert.Equal(t, int64(2), a.Violations)
		assert.Equal(t, int64(3), a.Visited)
	})

	// Backward skew, and truncated is the one field whose absence is not neutral: it qualifies
	// every other count as a floor. An envd that does not send it has not truncated, so false is
	// both the zero value and the correct reading -- which is why it is a bool and not a count.
	t.Run("an absent truncated flag reads as complete", func(t *testing.T) {
		t.Parallel()

		var a freezeAudit
		require.NoError(t, json.Unmarshal(
			[]byte(`{"visited":4,"frozen":4,"escaped":0,"violations":0}`), &a))
		assert.False(t, a.Truncated)
	})

	// And a header that is not JSON at all must be recognisable as a decode failure rather than
	// as a clean audit, which is what the silent all-or-nothing parser it replaced looked like.
	t.Run("a malformed header is an error, not an empty audit", func(t *testing.T) {
		t.Parallel()

		var a freezeAudit
		require.Error(t, json.Unmarshal([]byte("visited=12,frozen=5"), &a))
	})
}
