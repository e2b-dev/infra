package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/envd/internal/services/cgroups"
	"github.com/e2b-dev/infra/packages/envd/pkg"
)

// TestPostInit_ReportsEnvdVersion verifies /init always advertises the running
// envd version via the X-Envd-Version header. The orchestrator reads it off the
// resume-path /init it already makes, to decide, label, and confirm live
// upgrades against the actual running version. The header is set before any
// parsing/auth, so it is present regardless of the request outcome.
func TestPostInit_ReportsEnvdVersion(t *testing.T) {
	t.Parallel()

	api := newAPIWithCgroupManager(cgroups.NewNoopManager())

	// A malformed body makes PostInit return early — the header must still be set.
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "/init", bytes.NewReader([]byte("{not json")))
	require.NoError(t, err)
	rec := httptest.NewRecorder()

	api.PostInit(rec, req)

	assert.Equal(t, pkg.Version, rec.Header().Get("X-Envd-Version"))
}

// TestPostInit_ReportsHandover verifies /init advertises the live-upgrade
// handover outcome via X-Envd-Handover once SetHandoverResult has been called
// (and omits it otherwise), so the orchestrator can record what the new envd
// re-adopted — otherwise only logged in-guest.
func TestPostInit_ReportsHandover(t *testing.T) {
	t.Parallel()

	postInit := func(a *API) *httptest.ResponseRecorder {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "/init", bytes.NewReader([]byte("{not json")))
		require.NoError(t, err)
		rec := httptest.NewRecorder()
		a.PostInit(rec, req)

		return rec
	}

	// No handover happened: the header is absent.
	plain := newAPIWithCgroupManager(cgroups.NewNoopManager())
	assert.Empty(t, postInit(plain).Header().Get("X-Envd-Handover"))

	// After a handover: the outcome is advertised as JSON.
	upgraded := newAPIWithCgroupManager(cgroups.NewNoopManager())
	upgraded.SetHandoverResult(3, 1, 2, 1, 6, 1)
	assert.JSONEq(t,
		`{"procs":3,"procs_failed":1,"retained":2,"retained_failed":1,"watchers":6,"watchers_failed":1}`,
		postInit(upgraded).Header().Get("X-Envd-Handover"))
}
