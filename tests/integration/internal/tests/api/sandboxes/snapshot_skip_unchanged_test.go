package sandboxes

// Integration tests for the skipIfUnchanged path on
// POST /sandboxes/{id}/snapshots. See issue e2b-dev/infra#2580 and the
// orchestrator implementation in
// packages/orchestrator/pkg/server/sandboxes.go (Server.Checkpoint).
//
// These tests are permissive on the *outcome* of the inspector
// decision (unchanged=true vs false) because the in-guest tracker is
// only active when envd was built with -tags inspector_bpf AND the
// guest kernel supports the necessary BPF / soft-dirty primitives.
// What we DO assert is that:
//
//   - the new request field is correctly forwarded;
//   - the new response field is well-formed (nil OR a bool); and
//   - default-behavior parity (skipIfUnchanged unset) is preserved.

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/tests/integration/internal/api"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"
	"github.com/e2b-dev/infra/tests/integration/internal/utils"
)

func ptrBool(b bool) *bool { return &b }

// createSnapshotWithSkipFlag posts to /snapshots with an explicit
// skipIfUnchanged value (or nil for the default).
func createSnapshotWithSkipFlag(t *testing.T, c *api.ClientWithResponses, sbxID string, skip *bool) *api.PostSandboxesSandboxIDSnapshotsResponse {
	t.Helper()

	body := api.PostSandboxesSandboxIDSnapshotsJSONRequestBody{}
	if skip != nil {
		body.SkipIfUnchanged = skip
	}
	resp, err := c.PostSandboxesSandboxIDSnapshotsWithResponse(
		t.Context(),
		sbxID,
		body,
		setup.WithAPIKey(),
	)
	require.NoError(t, err)
	return resp
}

// TestSnapshotSkipIfUnchanged_DefaultOmittedField verifies that omitting
// skipIfUnchanged keeps the historical behavior. This is a regression
// guard for everyone NOT using the new feature.
func TestSnapshotSkipIfUnchanged_DefaultOmittedField(t *testing.T) {
	t.Parallel()
	c := setup.GetAPIClient()
	sbx := utils.SetupSandboxWithCleanup(t, c, utils.WithAutoPause(false))

	resp := createSnapshotWithSkipFlag(t, c, sbx.SandboxID, nil)
	require.Equal(t, http.StatusCreated, resp.StatusCode())
	require.NotNil(t, resp.JSON201)

	// Default path must not signal "unchanged" — that's an opt-in
	// behavior. The pointer should be nil or, if marshaled by some
	// future framework change, false; never true.
	if resp.JSON201.Unchanged != nil {
		assert.False(t, *resp.JSON201.Unchanged,
			"unchanged must not surface when skipIfUnchanged is omitted")
	}

	t.Cleanup(func() {
		c.DeleteTemplatesTemplateIDWithResponse(t.Context(), resp.JSON201.SnapshotID, setup.WithAPIKey())
	})
}

// TestSnapshotSkipIfUnchanged_FirstCallNeverSkips verifies that the
// FIRST call against a sandbox cannot short-circuit (there's no prior
// snapshot to skip to). This holds regardless of whether the inspector
// is loaded — it's a property of the orchestrator's logic.
func TestSnapshotSkipIfUnchanged_FirstCallNeverSkips(t *testing.T) {
	t.Parallel()
	c := setup.GetAPIClient()
	sbx := utils.SetupSandboxWithCleanup(t, c, utils.WithAutoPause(false))

	resp := createSnapshotWithSkipFlag(t, c, sbx.SandboxID, ptrBool(true))
	require.Equal(t, http.StatusCreated, resp.StatusCode())
	require.NotNil(t, resp.JSON201)

	if resp.JSON201.Unchanged != nil {
		assert.False(t, *resp.JSON201.Unchanged,
			"the first snapshot of a fresh sandbox cannot short-circuit")
	}

	t.Cleanup(func() {
		c.DeleteTemplatesTemplateIDWithResponse(t.Context(), resp.JSON201.SnapshotID, setup.WithAPIKey())
	})
}

// TestSnapshotSkipIfUnchanged_TwoCallsAfterWrite is the negative case
// for the skip path: between two snapshot calls we touch a file,
// and the second call must return a fresh snapshot (not the prior).
//
// This test is sensitive to whether the orchestrator has
// InspectorSkipUnchangedFlag on AND the in-guest tracker is loaded;
// when neither is true the second call always falls through to a full
// snapshot, which already satisfies the assertions below.
func TestSnapshotSkipIfUnchanged_TwoCallsAfterWrite(t *testing.T) {
	t.Parallel()
	c := setup.GetAPIClient()
	envdC := setup.GetEnvdClient(t, t.Context())
	sbx := utils.SetupSandboxWithCleanup(t, c, utils.WithAutoPause(false))

	// First call seeds the orchestrator's lastPublishedSnapshot.
	first := createSnapshotWithSkipFlag(t, c, sbx.SandboxID, ptrBool(true))
	require.Equal(t, http.StatusCreated, first.StatusCode())
	require.NotNil(t, first.JSON201)
	t.Cleanup(func() {
		c.DeleteTemplatesTemplateIDWithResponse(t.Context(), first.JSON201.SnapshotID, setup.WithAPIKey())
	})

	// Make a recovery-relevant change: write a file in the user's home.
	require.NoError(t,
		utils.ExecCommand(t, t.Context(), &api.Sandbox{SandboxID: sbx.SandboxID}, envdC,
			"sh", "-c", "echo hello > /tmp/skip-if-unchanged-witness"))

	// Second call: must not return Unchanged=true regardless of
	// whether the tracker is loaded.
	second := createSnapshotWithSkipFlag(t, c, sbx.SandboxID, ptrBool(true))
	require.Equal(t, http.StatusCreated, second.StatusCode())
	require.NotNil(t, second.JSON201)
	t.Cleanup(func() {
		c.DeleteTemplatesTemplateIDWithResponse(t.Context(), second.JSON201.SnapshotID, setup.WithAPIKey())
	})

	if second.JSON201.Unchanged != nil {
		assert.False(t, *second.JSON201.Unchanged,
			"second snapshot after a real write must not be reported as unchanged")
	}
	assert.NotEqual(t, first.JSON201.SnapshotID, second.JSON201.SnapshotID,
		"after a write, the second call must produce a new template id")
}

// TestSnapshotSkipIfUnchanged_RoundTripFieldShape locks the wire shape:
// SnapshotInfo.unchanged is *bool (omitempty) and the request body
// accepts a literal `{"skipIfUnchanged": true}`.
//
// If somebody refactors the schema (e.g. flattens the field into a
// non-pointer bool), this test will catch it before it ships.
func TestSnapshotSkipIfUnchanged_RoundTripFieldShape(t *testing.T) {
	t.Parallel()
	c := setup.GetAPIClient()
	sbx := utils.SetupSandboxWithCleanup(t, c, utils.WithAutoPause(false))

	resp := createSnapshotWithSkipFlag(t, c, sbx.SandboxID, ptrBool(false))
	require.Equal(t, http.StatusCreated, resp.StatusCode())
	require.NotNil(t, resp.JSON201)

	// skipIfUnchanged=false MUST behave identically to omitting it.
	if resp.JSON201.Unchanged != nil {
		assert.False(t, *resp.JSON201.Unchanged)
	}

	t.Cleanup(func() {
		c.DeleteTemplatesTemplateIDWithResponse(t.Context(), resp.JSON201.SnapshotID, setup.WithAPIKey())
	})
}
