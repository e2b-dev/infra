package envd

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFreezeResult_VanishedFromAnOlderEnvd pins the half of the rollout that cannot be
// staged: a sandbox paused by an envd that predates the vanished counter, resumed by an
// orchestrator that reads it. The fleet is mixed for as long as the envd rollout takes, and
// paused sandboxes carry their binary in the snapshot, so this is the ordinary case rather
// than a transitional one.
//
// Absent must mean zero. Reported as anything else it would show up as a population on the
// outcome metric that no guest actually produced, which is the failure this whole change is
// about -- a count that describes the reader rather than the tree.
func TestFreezeResult_VanishedFromAnOlderEnvd(t *testing.T) {
	t.Parallel()

	t.Run("an envd that omits the field reports no vanishes", func(t *testing.T) {
		t.Parallel()

		var res FreezeResult
		require.NoError(t, json.Unmarshal([]byte(
			`{"mode":"hierarchy","requested":19,"frozen":18,"notFrozen":0,"failed":1}`), &res))

		assert.Zero(t, res.Vanished)
		// The positive half, on the same payload: absence of the new field must not have
		// disturbed the fields that were there, which is what a decode test that only
		// asserts a zero would let through.
		assert.Equal(t, 19, res.Requested)
		assert.Equal(t, 18, res.Frozen)
		assert.Equal(t, 1, res.Failed)
	})

	t.Run("an envd that reports the field is read through", func(t *testing.T) {
		t.Parallel()

		var res FreezeResult
		require.NoError(t, json.Unmarshal([]byte(
			`{"mode":"hierarchy","requested":19,"frozen":18,"notFrozen":0,"failed":0,"vanished":1}`), &res))

		assert.Equal(t, 1, res.Vanished)
		assert.Zero(t, res.Failed, "the outcome the count was previously landing in")
		assert.Zero(t, res.NotFrozen, "and the one an uncounted drop would have landed in")
	})
}
