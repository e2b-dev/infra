package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
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
	require.NoError(t, testDB.SqlcClient.TestsRawSQL(ctx, `
		CREATE TABLE public.agents (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			team_id UUID,
			name TEXT NOT NULL,
			template_id TEXT NOT NULL,
			description TEXT NOT NULL,
			command TEXT,
			author TEXT,
			public BOOLEAN NOT NULL DEFAULT FALSE,
			position INTEGER NOT NULL DEFAULT 0,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			deleted_at TIMESTAMPTZ
		)
	`))
	require.NoError(t, testDB.SqlcClient.TestsRawSQL(ctx, `
		INSERT INTO public.agents (team_id, name, template_id, description, command, author, public, position, deleted_at)
		VALUES
			(NULL, 'Deleted', 'deleted-template', 'Hidden agent.', NULL, NULL, TRUE, 0, now()),
			(NULL, 'Codex', 'codex-template', 'Codex CLI.', 'codex', 'E2B', TRUE, 10, NULL),
			($1, 'Claude', 'claude-template', 'Claude Code.', NULL, NULL, FALSE, 20, NULL),
			($2, 'Other Team Private', 'other-private-template', 'Hidden agent.', NULL, NULL, FALSE, 30, NULL),
			($2, 'Other Team Public', 'other-public-template', 'Hidden agent.', NULL, NULL, TRUE, 40, NULL)
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
	assert.True(t, resp.Agents[0].Public)
	assert.Nil(t, resp.Agents[0].TeamId)
	assert.False(t, resp.Agents[0].CreatedAt.IsZero())
	assert.False(t, resp.Agents[0].UpdatedAt.IsZero())
	assert.Nil(t, resp.Agents[0].DeletedAt)

	assert.NotEmpty(t, resp.Agents[1].Id)
	assert.Nil(t, resp.Agents[1].Command)
	assert.Nil(t, resp.Agents[1].Author)
	assert.False(t, resp.Agents[1].Public)
	assert.NotNil(t, resp.Agents[1].TeamId)
	assert.Equal(t, teamID, *resp.Agents[1].TeamId)
}
