package handlers

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/auth/pkg/auth"
	authtypes "github.com/e2b-dev/infra/packages/auth/pkg/types"
	authqueries "github.com/e2b-dev/infra/packages/db/pkg/auth/queries"
)

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
