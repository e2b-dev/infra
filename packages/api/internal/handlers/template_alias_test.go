package handlers

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	apispec "github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/auth"
	"github.com/e2b-dev/infra/packages/api/internal/db/types"
	"github.com/e2b-dev/infra/packages/db/client"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/db/testutils"
)

func TestQueryNotExistingTemplateAlias(t *testing.T) {
	testDB := testutils.SetupDatabase(t)

	store := &APIStore{
		sqlcDB: testDB,
	}

	alias := "non-existing-template-alias"
	team := createTestTeam(t, testDB)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/templates/aliases/%s", alias), nil)
	c.Set(
		auth.TeamContextKey,
		&types.Team{
			Team: &queries.Team{
				ID: team.ID,
			},
		},
	)

	store.GetTemplatesAliasesAlias(c, alias)

	res, err := apispec.ParseGetTemplatesAliasesAliasResponse(w.Result())
	require.NoError(t, err)
	require.Equal(t, http.StatusNotFound, res.StatusCode())
}

func TestQueryExistingTemplateAlias(t *testing.T) {
	testDB := testutils.SetupDatabase(t)

	alias := "some-alias"
	team := createTestTeam(t, testDB)

	_ = createTestTemplateWithAlias(t, testDB, team.ID, alias)

	store := &APIStore{
		sqlcDB: testDB,
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/templates/aliases/%s", alias), nil)
	c.Set(
		auth.TeamContextKey,
		&types.Team{
			Team: &queries.Team{
				ID: team.ID,
			},
		},
	)

	store.GetTemplatesAliasesAlias(c, alias)

	res, err := apispec.ParseGetTemplatesAliasesAliasResponse(w.Result())
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, res.StatusCode())
}

func TestQueryExistingTemplateAliasAsNotOwnerTeam(t *testing.T) {
	testDB := testutils.SetupDatabase(t)

	alias := "some-alias"
	ownerTeam := createTestTeam(t, testDB)
	foreignTeam := createTestTeam(t, testDB)

	_ = createTestTemplateWithAlias(t, testDB, ownerTeam.ID, alias)

	store := &APIStore{
		sqlcDB: testDB,
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/templates/aliases/%s", alias), nil)
	c.Set(
		auth.TeamContextKey,
		&types.Team{
			Team: &queries.Team{
				ID: foreignTeam.ID,
			},
		},
	)

	store.GetTemplatesAliasesAlias(c, alias)

	res, err := apispec.ParseGetTemplatesAliasesAliasResponse(w.Result())
	require.NoError(t, err)
	require.Equal(t, http.StatusForbidden, res.StatusCode())
}

func createTestTeam(t *testing.T, sqlcDB *client.Client) queries.Test_CreateTeamParams {
	t.Helper()

	teamID := uuid.New()
	req := queries.Test_CreateTeamParams{
		ID:      teamID,
		Email:   fmt.Sprintf("test-%s@e2b.dev", teamID),
		Name:    fmt.Sprintf("test-%s", teamID),
		Tier:    "base_v1",
		Blocked: false,
	}

	err := sqlcDB.Test_CreateTeam(t.Context(), req)
	require.NoError(t, err)

	return req
}

func createTestTemplateWithAlias(t *testing.T, sqlcDB *client.Client, teamID uuid.UUID, alias string) string {
	t.Helper()

	templateID := uuid.NewString()
	err := sqlcDB.CreateOrUpdateTemplate(
		t.Context(), queries.CreateOrUpdateTemplateParams{
			TemplateID: templateID,
			TeamID:     teamID,
			CreatedBy:  nil,
			ClusterID:  nil,
		},
	)
	require.NoError(t, err)

	err = sqlcDB.CreateTemplateAlias(
		t.Context(), queries.CreateTemplateAliasParams{
			Alias:      alias,
			TemplateID: templateID,
		},
	)
	require.NoError(t, err)

	return templateID
}
