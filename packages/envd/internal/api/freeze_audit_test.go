package api

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFreezeAuditHeader_WireFormat pins the exact bytes of X-Envd-Freeze-Audit. The orchestrator
// decodes this header in a different repository tree and a different release cadence, so the
// field names are a contract between two independently deployed components: rename one here and
// the fleet-wide audit signal goes quiet with nothing to indicate it. Its counterpart lives in
// the orchestrator's own test, asserting the same literal from the reading side.
func TestFreezeAuditHeader_WireFormat(t *testing.T) {
	t.Parallel()

	b, err := json.Marshal(freezeAudit{Visited: 12, Frozen: 5, Escaped: 1, Violations: 0, Truncated: true})
	require.NoError(t, err)
	assert.JSONEq(t,
		`{"visited":12,"frozen":5,"escaped":1,"violations":0,"truncated":true}`,
		string(b))

	// Zero counts must be PRESENT, not omitted: violations=0 is the audit's central result --
	// the statement that the allowlist held -- and a reader cannot distinguish an omitted field
	// from an envd too old to have it.
	b, err = json.Marshal(freezeAudit{})
	require.NoError(t, err)
	assert.JSONEq(t,
		`{"visited":0,"frozen":0,"escaped":0,"violations":0,"truncated":false}`,
		string(b))
}
