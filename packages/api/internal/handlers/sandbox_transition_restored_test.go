package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/auth/pkg/auth"
	authtypes "github.com/e2b-dev/infra/packages/auth/pkg/types"
	authqueries "github.com/e2b-dev/infra/packages/db/pkg/auth/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/id"
)

// resumeWaitStub scripts the record reads and the wait a resume of a Pausing
// sandbox goes through.
type resumeWaitStub struct {
	reads   []sandbox.Sandbox
	waitErr error
	calls   int
}

func (s *resumeWaitStub) GetSandbox(context.Context, uuid.UUID, string) (sandbox.Sandbox, error) {
	i := min(s.calls, len(s.reads)-1)
	s.calls++

	return s.reads[i], nil
}

func (s *resumeWaitStub) WaitForStateChange(context.Context, uuid.UUID, string) error {
	return s.waitErr
}

func postResume(t *testing.T, teamID uuid.UUID, sandboxID string, stub *resumeWaitStub) *httptest.ResponseRecorder {
	t.Helper()

	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	ginCtx.Request = httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/sandboxes/"+sandboxID+"/resume", nil)
	auth.SetTeamInfoForTest(t, ginCtx, &authtypes.Team{
		Team:   &authqueries.Team{ID: teamID, Slug: "test-team"},
		Limits: &authtypes.TeamLimits{MaxLengthHours: 24},
	})

	store := &APIStore{resumeBackendOverride: stub}
	store.PostSandboxesSandboxIDResume(ginCtx, sandboxID)

	return recorder
}

// A resume that waited on a pause the node refused answers 409 already
// running — the sandbox is healthy — never 500.
func TestResume_RestoredPauseAnswersAlreadyRunning(t *testing.T) {
	t.Parallel()

	teamID := uuid.New()
	sandboxID := "i" + id.Generate()
	pausing := sandbox.Sandbox{SandboxID: sandboxID, TeamID: teamID, State: sandbox.StatePausing}

	recorder := postResume(t, teamID, sandboxID, &resumeWaitStub{reads: []sandbox.Sandbox{pausing}, waitErr: sandbox.ErrTransitionRestored})

	require.Equal(t, http.StatusConflict, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "already running")
}

// The transition can complete before the wait looks and report nothing; the
// re-read finds the sandbox running and answers 409 instead of starting a
// second instance.
func TestResume_CompletedRestoreIsCaughtByTheReRead(t *testing.T) {
	t.Parallel()

	teamID := uuid.New()
	sandboxID := "i" + id.Generate()
	pausing := sandbox.Sandbox{SandboxID: sandboxID, TeamID: teamID, State: sandbox.StatePausing}
	running := pausing
	running.State = sandbox.StateRunning

	recorder := postResume(t, teamID, sandboxID, &resumeWaitStub{reads: []sandbox.Sandbox{pausing, running}, waitErr: nil})

	require.Equal(t, http.StatusConflict, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "already running")
}

// connectStub answers "pausing" until the wait reports the restore, then
// answers with the running sandbox.
type connectStub struct {
	sbx      sandbox.Sandbox
	waited   bool
	waitErr  error
	waitCall int
}

func (s *connectStub) KeepAliveFor(_ context.Context, _ uuid.UUID, sandboxID string, _ time.Duration, _ bool) (*sandbox.Sandbox, *api.APIError) {
	if !s.waited {
		return nil, &api.APIError{Code: http.StatusConflict, Err: &sandbox.NotRunningError{SandboxID: sandboxID, State: sandbox.StatePausing}, ClientMsg: "pausing"}
	}

	return &s.sbx, nil
}

func (s *connectStub) WaitForStateChange(context.Context, uuid.UUID, string) error {
	s.waitCall++
	s.waited = true

	return s.waitErr
}

// A connect that waited on a refused-and-restored pause re-reads and connects
// to the running sandbox.
func TestConnect_RestoredPauseConnects(t *testing.T) {
	t.Parallel()

	teamID := uuid.New()
	sandboxID := "i" + id.Generate()
	stub := &connectStub{
		sbx:     sandbox.Sandbox{SandboxID: sandboxID, TeamID: teamID, State: sandbox.StateRunning, StartTime: time.Now(), EndTime: time.Now().Add(time.Hour)},
		waitErr: sandbox.ErrTransitionRestored,
	}

	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	ginCtx.Request = httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/sandboxes/"+sandboxID+"/connect", strings.NewReader(`{"timeout":60}`))
	ginCtx.Request.Header.Set("Content-Type", "application/json")
	auth.SetTeamInfoForTest(t, ginCtx, &authtypes.Team{
		Team:   &authqueries.Team{ID: teamID, Slug: "test-team"},
		Limits: &authtypes.TeamLimits{MaxLengthHours: 24},
	})

	store := &APIStore{connectBackendOverride: stub}
	store.PostSandboxesSandboxIDConnect(ginCtx, sandboxID)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	assert.Equal(t, 1, stub.waitCall)
}
