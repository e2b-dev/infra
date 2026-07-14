package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	handlersmocks "github.com/e2b-dev/infra/packages/api/internal/handlers/mocks"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/auth/pkg/auth"
	authtypes "github.com/e2b-dev/infra/packages/auth/pkg/types"
	authqueries "github.com/e2b-dev/infra/packages/db/pkg/auth/queries"
	"github.com/e2b-dev/infra/packages/db/pkg/testutils"
)

// newCrossTeamSnapshotFixture creates a snapshot owned by one team and returns an
// APIStore whose orchestrator is mocked (so the sandbox is reported not-running and
// the handler falls through to the snapshot lookup) together with a *different*
// requesting team. The returned sandboxID is a bare, dash-free lowercase ID so it
// survives utils.ShortID parsing unchanged and matches the stored snapshot row.
func newCrossTeamSnapshotFixture(t *testing.T) (store *APIStore, orch *handlersmocks.MockSandboxOrchestrator, requesterTeamID uuid.UUID, sandboxID string) {
	t.Helper()

	db := testutils.SetupDatabase(t)
	ctx := t.Context()

	sc := newTestSnapshotCache(t, db)
	orch = handlersmocks.NewMockSandboxOrchestrator(t)

	ownerTeamID := testutils.CreateTestTeam(t, db)
	baseTemplateID := testutils.CreateTestTemplate(t, db, ownerTeamID)

	sandboxID = strings.ReplaceAll(uuid.NewString(), "-", "")
	snapshotTemplateID := "snapshot-template-" + uuid.New().String()
	testutils.UpsertTestSnapshot(t, ctx, db, snapshotTemplateID, sandboxID, ownerTeamID, baseTemplateID)

	requesterTeamID = testutils.CreateTestTeam(t, db)

	store = &APIStore{
		orchestrator:  orch,
		snapshotCache: sc,
	}

	return store, orch, requesterTeamID, sandboxID
}

func newTeamGinContext(t *testing.T, teamID uuid.UUID, method, target string, body any) (*httptest.ResponseRecorder, *gin.Context) {
	t.Helper()

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	var raw []byte
	if body != nil {
		var err error
		raw, err = json.Marshal(body)
		require.NoError(t, err)
	}

	c.Request = httptest.NewRequestWithContext(t.Context(), method, target, bytes.NewReader(raw))
	c.Request.Header.Set("Content-Type", "application/json")

	auth.SetTeamInfoForTest(t, c, &authtypes.Team{
		Team:   &authqueries.Team{ID: teamID, Slug: "team-" + teamID.String()[:8]},
		Limits: &authtypes.TeamLimits{MaxLengthHours: 24},
	})

	return rec, c
}

// TestGetSandboxesSandboxID_CrossTeamSnapshotReturnsNotFound verifies ENG-3544 end to
// end for GET: when the running sandbox is absent and the snapshot belongs to another
// team, the requester receives 404 rather than the other team's snapshot.
func TestGetSandboxesSandboxID_CrossTeamSnapshotReturnsNotFound(t *testing.T) {
	t.Parallel()

	store, orch, requesterTeamID, sandboxID := newCrossTeamSnapshotFixture(t)

	orch.EXPECT().
		GetSandbox(mock.Anything, requesterTeamID, sandboxID).
		Return(sandbox.Sandbox{}, sandbox.ErrNotFound)

	rec, c := newTeamGinContext(t, requesterTeamID, http.MethodGet, "/sandboxes/"+sandboxID, nil)
	store.GetSandboxesSandboxID(c, sandboxID)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// TestPostSandboxesSandboxIDConnect_CrossTeamSnapshotReturnsNotFound verifies the same
// for the connect path: KeepAliveFor reports the sandbox is not in the store, so the
// handler falls through to the team-scoped snapshot lookup, which must not find another
// team's snapshot.
func TestPostSandboxesSandboxIDConnect_CrossTeamSnapshotReturnsNotFound(t *testing.T) {
	t.Parallel()

	store, orch, requesterTeamID, sandboxID := newCrossTeamSnapshotFixture(t)

	orch.EXPECT().
		KeepAliveFor(mock.Anything, requesterTeamID, sandboxID, mock.Anything, false).
		Return(nil, &api.APIError{Code: http.StatusNotFound, ClientMsg: "not found", Err: sandbox.ErrNotFound})

	rec, c := newTeamGinContext(t, requesterTeamID, http.MethodPost, "/sandboxes/"+sandboxID+"/connect", api.ConnectSandbox{Timeout: 30})
	store.PostSandboxesSandboxIDConnect(c, sandboxID)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// TestPostSandboxesSandboxIDResume_CrossTeamSnapshotReturnsNotFound verifies the same
// for the resume path: GetSandbox reports not-running, and the team-scoped snapshot
// lookup must not resume another team's snapshot.
func TestPostSandboxesSandboxIDResume_CrossTeamSnapshotReturnsNotFound(t *testing.T) {
	t.Parallel()

	store, orch, requesterTeamID, sandboxID := newCrossTeamSnapshotFixture(t)

	orch.EXPECT().
		GetSandbox(mock.Anything, requesterTeamID, sandboxID).
		Return(sandbox.Sandbox{}, sandbox.ErrNotFound)

	rec, c := newTeamGinContext(t, requesterTeamID, http.MethodPost, "/sandboxes/"+sandboxID+"/resume", api.ResumedSandbox{})
	store.PostSandboxesSandboxIDResume(c, sandboxID)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}
