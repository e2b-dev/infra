package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/auth/pkg/auth"
	"github.com/e2b-dev/infra/packages/dashboard-api/internal/api"
	"github.com/e2b-dev/infra/packages/db/pkg/testutils"
)

func TestGetAgents_ReturnsPublicAndTeamAgentsInOrder(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)
	ctx := t.Context()
	teamID := testutils.CreateTestTeam(t, testDB)
	otherTeamID := testutils.CreateTestTeam(t, testDB)
	createAgentsTable(t, testDB)
	require.NoError(t, testDB.SqlcClient.TestsRawSQL(ctx, `
		INSERT INTO public.agents (team_id, name, template_id, description, command, author, public, published_at, position, deleted_at)
		VALUES
			(NULL, 'Deleted', 'deleted-template', 'Hidden agent.', NULL, NULL, TRUE, now(), 0, now()),
			(NULL, 'Unpublished', 'unpublished-template', 'Hidden agent.', NULL, NULL, TRUE, NULL, 5, NULL),
			(NULL, 'Codex', 'codex-template', 'Codex CLI.', 'codex', 'E2B', TRUE, now(), 10, NULL),
			($1, 'Claude', 'claude-template', 'Claude Code.', NULL, NULL, FALSE, now(), 20, NULL),
			($2, 'Other Team Private', 'other-private-template', 'Hidden agent.', NULL, NULL, FALSE, now(), 30, NULL),
			($2, 'Other Team Public', 'other-public-template', 'Hidden agent.', NULL, NULL, TRUE, now(), 40, NULL)
	`, teamID, otherTeamID))

	store := &APIStore{db: testDB.SqlcClient}
	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	ginCtx.Request = httptest.NewRequestWithContext(ctx, http.MethodGet, "/agents", nil)
	auth.SetTeamInfoForTest(t, ginCtx, teamInfo(teamID, nil))

	store.GetAgents(ginCtx)

	require.Equal(t, http.StatusOK, recorder.Code)

	var resp api.AgentsResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &resp))
	require.Len(t, resp.Agents, 2)

	assert.NotEmpty(t, resp.Agents[0].Id)
	assert.Equal(t, "codex-template", resp.Agents[0].Template)
	assert.NotNil(t, resp.Agents[0].Command)
	assert.Equal(t, "codex", *resp.Agents[0].Command)
	assert.NotNil(t, resp.Agents[0].Author)
	assert.Equal(t, "E2B", *resp.Agents[0].Author)
	assert.Empty(t, resp.Agents[0].Metadata)
	assert.True(t, resp.Agents[0].Public)
	assert.NotNil(t, resp.Agents[0].PublishedAt)
	assert.Nil(t, resp.Agents[0].TeamId)
	assert.False(t, resp.Agents[0].CreatedAt.IsZero())
	assert.False(t, resp.Agents[0].UpdatedAt.IsZero())
	assert.Nil(t, resp.Agents[0].DeletedAt)

	assert.NotEmpty(t, resp.Agents[1].Id)
	assert.Nil(t, resp.Agents[1].Command)
	assert.Nil(t, resp.Agents[1].Author)
	assert.False(t, resp.Agents[1].Public)
	assert.NotNil(t, resp.Agents[1].PublishedAt)
	assert.NotNil(t, resp.Agents[1].TeamId)
	assert.Equal(t, teamID, *resp.Agents[1].TeamId)
}

func TestPostAgents_CreatesTeamAgentWithMetadata(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)
	ctx := t.Context()
	teamID := testutils.CreateTestTeam(t, testDB)
	createAgentsTable(t, testDB)

	store := &APIStore{db: testDB.SqlcClient}
	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	ginCtx.Request = httptest.NewRequestWithContext(ctx, http.MethodPost, "/agents", strings.NewReader(`{
		"name": "Codex",
		"template": "codex-template",
		"description": "Codex CLI.",
		"command": "codex",
		"author": "E2B",
		"metadata": {"icon": "terminal", "priority": 10},
		"public": true,
		"publishedAt": "2026-06-15T12:00:00Z"
	}`))
	ginCtx.Request.Header.Set("Content-Type", "application/json")
	auth.SetTeamInfoForTest(t, ginCtx, teamInfo(teamID, nil))

	store.PostAgents(ginCtx)

	require.Equal(t, http.StatusCreated, recorder.Code, recorder.Body.String())

	var resp api.Agent
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &resp))
	assert.Equal(t, "Codex", resp.Name)
	assert.Equal(t, "codex-template", resp.Template)
	assert.NotNil(t, resp.TeamId)
	assert.Equal(t, teamID, *resp.TeamId)
	assert.True(t, resp.Public)
	require.NotNil(t, resp.PublishedAt)
	assert.Equal(t, "2026-06-15T12:00:00Z", resp.PublishedAt.UTC().Format(time.RFC3339))
	assert.Equal(t, "terminal", resp.Metadata["icon"])
	assert.Equal(t, float64(10), resp.Metadata["priority"])
}

func TestPatchAgentsAgentID_UpdatesTeamAgentAndClearsNullableFields(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)
	ctx := t.Context()
	teamID := testutils.CreateTestTeam(t, testDB)
	createAgentsTable(t, testDB)
	agentID := uuid.New()
	require.NoError(t, testDB.SqlcClient.TestsRawSQL(ctx, `
		INSERT INTO public.agents (id, team_id, name, template_id, description, command, author, metadata, public, published_at)
		VALUES ($1, $2, 'Codex', 'codex-template', 'Codex CLI.', 'codex', 'E2B', '{"icon":"terminal"}', false, now())
	`, agentID, teamID))

	store := &APIStore{db: testDB.SqlcClient}
	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	ginCtx.Request = httptest.NewRequestWithContext(ctx, http.MethodPatch, "/agents/"+agentID.String(), strings.NewReader(`{
		"name": "Codex CLI",
		"command": null,
		"author": null,
		"metadata": {"icon": "sparkles"},
		"public": true,
		"publishedAt": null
	}`))
	ginCtx.Request.Header.Set("Content-Type", "application/json")
	auth.SetTeamInfoForTest(t, ginCtx, teamInfo(teamID, nil))

	store.PatchAgentsAgentID(ginCtx, agentID)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())

	var resp api.Agent
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &resp))
	assert.Equal(t, agentID, uuid.UUID(resp.Id))
	assert.Equal(t, "Codex CLI", resp.Name)
	assert.Equal(t, "codex-template", resp.Template)
	assert.Nil(t, resp.Command)
	assert.Nil(t, resp.Author)
	assert.True(t, resp.Public)
	assert.Nil(t, resp.PublishedAt)
	assert.Equal(t, "sparkles", resp.Metadata["icon"])
}

func TestDeleteAgentsAgentID_SoftDeletesTeamAgent(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)
	ctx := t.Context()
	teamID := testutils.CreateTestTeam(t, testDB)
	createAgentsTable(t, testDB)
	agentID := uuid.New()
	require.NoError(t, testDB.SqlcClient.TestsRawSQL(ctx, `
		INSERT INTO public.agents (id, team_id, name, template_id, description)
		VALUES ($1, $2, 'Codex', 'codex-template', 'Codex CLI.')
	`, agentID, teamID))

	store := &APIStore{db: testDB.SqlcClient}
	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	ginCtx.Request = httptest.NewRequestWithContext(ctx, http.MethodDelete, "/agents/"+agentID.String(), nil)
	auth.SetTeamInfoForTest(t, ginCtx, teamInfo(teamID, nil))

	store.DeleteAgentsAgentID(ginCtx, agentID)

	require.Equal(t, http.StatusNoContent, recorder.Code, recorder.Body.String())

	recorder = httptest.NewRecorder()
	ginCtx, _ = gin.CreateTestContext(recorder)
	ginCtx.Request = httptest.NewRequestWithContext(ctx, http.MethodDelete, "/agents/"+agentID.String(), nil)
	auth.SetTeamInfoForTest(t, ginCtx, teamInfo(teamID, nil))

	store.DeleteAgentsAgentID(ginCtx, agentID)

	assert.Equal(t, http.StatusNotFound, recorder.Code)
}

func createAgentsTable(t *testing.T, testDB *testutils.Database) {
	t.Helper()

	require.NoError(t, testDB.SqlcClient.TestsRawSQL(t.Context(), `
		CREATE TABLE public.agents (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			team_id UUID,
			name TEXT NOT NULL,
			template_id TEXT NOT NULL,
			description TEXT NOT NULL,
			command TEXT,
			author TEXT,
			metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
			public BOOLEAN NOT NULL DEFAULT FALSE,
			position INTEGER NOT NULL DEFAULT 0,
			published_at TIMESTAMPTZ,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			deleted_at TIMESTAMPTZ
		)
	`))
}
