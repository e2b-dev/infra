//go:build linux

package fc

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Regression: when hostBefore was already freePageHintDone (steady state
// after a prior cycle, common after resume-from-snapshot) and the new
// cycle completed between polls, an earlier sawBump check missed the
// transition and hung until ctx timeout.
func TestPollFphDone_FastCycleAfterPriorDone(t *testing.T) {
	t.Parallel()

	calls := 0
	describe := func(_ context.Context) (int64, error) {
		calls++

		return freePageHintDone, nil
	}

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()

	require.NoError(t, pollFphDone(ctx, describe))
	assert.Equal(t, 1, calls)
}
