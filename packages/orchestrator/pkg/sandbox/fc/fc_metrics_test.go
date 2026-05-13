//go:build linux

package fc

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Confirms the JSON tags on firecrackerBalloonMetrics match the keys FC
// emits, so the reader actually populates the snapshot in production.
func TestFirecrackerMetrics_ParsesBalloonLine(t *testing.T) {
	t.Parallel()

	const line = `{
		"net": {},
		"block": {},
		"balloon": {
			"free_page_hint_count": 11,
			"free_page_hint_freed": 46137344,
			"free_page_report_count": 2,
			"free_page_report_freed": 8388608
		}
	}`

	var m firecrackerMetrics
	require.NoError(t, json.Unmarshal([]byte(line), &m))

	snap := accumulateBalloon(nil, m.Balloon)
	assert.Equal(t, uint64(11), snap.HintCount)
	assert.Equal(t, uint64(46137344), snap.HintFreed)
	assert.Equal(t, uint64(2), snap.ReportCount)
	assert.Equal(t, uint64(8388608), snap.ReportFreed)
}
