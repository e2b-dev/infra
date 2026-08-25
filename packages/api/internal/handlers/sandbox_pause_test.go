package handlers

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/auth/pkg/auth"
	authtypes "github.com/e2b-dev/infra/packages/auth/pkg/types"
	authqueries "github.com/e2b-dev/infra/packages/db/pkg/auth/queries"
)

// TestFsOnlyPauseGate pins the pre-commit gate's four outcomes. The gate is
// what stands between a clean refusal (sandbox untouched) and a stranded VM:
// RemoveSandbox tears down routing and store state regardless of the
// orchestrator RPC's result, so every branch's decision matters. The version
// check is exact — versions here are real, no resolution is consulted.
func TestFsOnlyPauseGate(t *testing.T) {
	t.Parallel()

	lookup := func(sbx sandbox.Sandbox, err error) func(context.Context) (sandbox.Sandbox, error) {
		return func(context.Context) (sandbox.Sandbox, error) { return sbx, err }
	}

	t.Run("qualifying version proceeds", func(t *testing.T) {
		t.Parallel()

		apiErr := fsOnlyPauseGate(t.Context(),
			lookup(sandbox.Sandbox{FirecrackerVersion: "v1.14-0.1.1"}, nil), "sbx")
		assert.Nil(t, apiErr)
	})

	t.Run("non-qualifying version refuses with 409", func(t *testing.T) {
		t.Parallel()

		apiErr := fsOnlyPauseGate(t.Context(),
			lookup(sandbox.Sandbox{FirecrackerVersion: "v1.14.1_431f1fc"}, nil), "sbx")
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusConflict, apiErr.Code)
		assert.Contains(t, apiErr.ClientMsg, "memory: false")
	})

	t.Run("not-found falls through to RemoveSandbox's error surface", func(t *testing.T) {
		t.Parallel()

		apiErr := fsOnlyPauseGate(t.Context(),
			lookup(sandbox.Sandbox{}, sandbox.ErrNotFound), "sbx")
		assert.Nil(t, apiErr, "not-found must proceed so already-paused/not-found keep their canonical responses")
	})

	t.Run("generic lookup failure refuses with 500 instead of proceeding unchecked", func(t *testing.T) {
		t.Parallel()

		apiErr := fsOnlyPauseGate(t.Context(),
			lookup(sandbox.Sandbox{FirecrackerVersion: "v1.14-0.1.1"}, errors.New("store unavailable")), "sbx")
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusInternalServerError, apiErr.Code)
	})
}

// fakePauseBackend records the pause handler's calls against it.
type fakePauseBackend struct {
	sbx       sandbox.Sandbox
	lookupErr error

	removeCalled bool
	removeOpts   sandbox.RemoveOpts
}

func (f *fakePauseBackend) GetSandbox(context.Context, uuid.UUID, string) (sandbox.Sandbox, error) {
	return f.sbx, f.lookupErr
}

func (f *fakePauseBackend) RemoveSandbox(_ context.Context, _ uuid.UUID, _ string, opts sandbox.RemoveOpts) error {
	f.removeCalled = true
	f.removeOpts = opts

	return nil
}

func pauseRequest(t *testing.T, teamID uuid.UUID, body string) (*httptest.ResponseRecorder, *gin.Context) {
	t.Helper()

	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	ginCtx.Request = httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/sandboxes/sbx/pause", bytes.NewReader([]byte(body)))
	ginCtx.Request.Header.Set("Content-Type", "application/json")
	auth.SetTeamInfoForTest(t, ginCtx, &authtypes.Team{
		Team:   &authqueries.Team{ID: teamID},
		Limits: &authtypes.TeamLimits{MaxLengthHours: 24},
	})

	return recorder, ginCtx
}

// TestPostSandboxesSandboxIDPause_FsOnlyGateWiring pins the load-bearing
// handler wiring the seam test cannot: on a refused fs-only pause,
// RemoveSandbox must NOT be called — the refusal is only worth anything if it
// lands before the pause chain commits. The qualifying case pins the inverse:
// the pause proceeds with FilesystemOnly threaded through.
func TestPostSandboxesSandboxIDPause_FsOnlyGateWiring(t *testing.T) {
	t.Parallel()

	// 20-char sandbox IDs pass utils.ShortID untruncated.
	const sandboxID = "sbxwiringtest0000000"

	t.Run("refusal must not reach RemoveSandbox", func(t *testing.T) {
		t.Parallel()

		backend := &fakePauseBackend{sbx: sandbox.Sandbox{FirecrackerVersion: "v1.14.1_431f1fc"}}
		store := &APIStore{pauseBackendOverride: backend}

		recorder, ginCtx := pauseRequest(t, uuid.New(), `{"memory": false}`)
		store.PostSandboxesSandboxIDPause(ginCtx, sandboxID)

		assert.Equal(t, http.StatusConflict, recorder.Code)
		assert.False(t, backend.removeCalled,
			"a refused fs-only pause must never commit the pause chain — RemoveSandbox tears down routing regardless of the RPC outcome")
	})

	t.Run("qualifying version proceeds into RemoveSandbox with filesystem_only", func(t *testing.T) {
		t.Parallel()

		backend := &fakePauseBackend{sbx: sandbox.Sandbox{FirecrackerVersion: "v1.14-0.1.1", FirecrackerVersionResolved: true}}
		store := &APIStore{pauseBackendOverride: backend}

		_, ginCtx := pauseRequest(t, uuid.New(), `{"memory": false}`)
		store.PostSandboxesSandboxIDPause(ginCtx, sandboxID)

		// gin's test context does not flush a body-less status to the
		// recorder; read it off the writer.
		assert.Equal(t, http.StatusNoContent, ginCtx.Writer.Status())
		require.True(t, backend.removeCalled)
		assert.True(t, backend.removeOpts.FilesystemOnly)
		assert.Equal(t, sandbox.StateActionPause, backend.removeOpts.Action)
	})

	t.Run("memory pause skips the gate entirely", func(t *testing.T) {
		t.Parallel()

		// A legacy version must not block a plain memory pause.
		backend := &fakePauseBackend{sbx: sandbox.Sandbox{FirecrackerVersion: "v1.14.1_431f1fc"}}
		store := &APIStore{pauseBackendOverride: backend}

		_, ginCtx := pauseRequest(t, uuid.New(), ``)
		store.PostSandboxesSandboxIDPause(ginCtx, sandboxID)

		assert.Equal(t, http.StatusNoContent, ginCtx.Writer.Status())
		require.True(t, backend.removeCalled)
		assert.False(t, backend.removeOpts.FilesystemOnly)
	})
}
