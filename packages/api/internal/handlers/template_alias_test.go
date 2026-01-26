package handlers

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	apispec "github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/auth"
	"github.com/e2b-dev/infra/packages/api/internal/db/types"
	authqueries "github.com/e2b-dev/infra/packages/db/pkg/auth/queries"
	"github.com/e2b-dev/infra/packages/db/pkg/testutils"
)

func TestQueryNotExistingTemplateAlias(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)

	store := &APIStore{
		sqlcDB: testDB.SqlcClient,
		authDB: testDB.AuthDb,
	}

	alias := "non-existing-template-alias"
	teamID := testutils.CreateTestTeam(t, testDB)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/templates/aliases/%s", alias), nil)
	c.Set(
		auth.TeamContextKey,
		&types.Team{
			Team: &authqueries.Team{
				ID: teamID,
			},
		},
	)

	store.GetTemplatesAliasesAlias(c, alias)

	res, err := apispec.ParseGetTemplatesAliasesAliasResponse(w.Result())
	require.NoError(t, err)
	require.Equal(t, http.StatusNotFound, res.StatusCode())
}

func TestQueryExistingTemplateAlias(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)

	teamID := testutils.CreateTestTeam(t, testDB)
	templateID, alias := testutils.CreateTestTemplateWithAlias(t, testDB, teamID)

	store := &APIStore{
		sqlcDB: testDB.SqlcClient,
		authDB: testDB.AuthDb,
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/templates/aliases/%s", alias), nil)
	c.Set(
		auth.TeamContextKey,
		&types.Team{
			Team: &authqueries.Team{
				ID: teamID,
			},
		},
	)

	store.GetTemplatesAliasesAlias(c, alias)

	res, err := apispec.ParseGetTemplatesAliasesAliasResponse(w.Result())
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, res.StatusCode())

	require.NotNil(t, res.JSON200)

	resBody := *res.JSON200
	assert.Equal(t, templateID, resBody.TemplateID)
	assert.True(t, resBody.Public)
}

func TestQueryExistingTemplateAliasAsNotOwnerTeam(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)

	ownerTeamID := testutils.CreateTestTeam(t, testDB)
	foreignTeamID := testutils.CreateTestTeam(t, testDB)
	_, alias := testutils.CreateTestTemplateWithAlias(t, testDB, ownerTeamID)

	store := &APIStore{
		sqlcDB: testDB.SqlcClient,
		authDB: testDB.AuthDb,
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/templates/aliases/%s", alias), nil)
	c.Set(
		auth.TeamContextKey,
		&types.Team{
			Team: &authqueries.Team{
				ID: foreignTeamID,
			},
		},
	)

	store.GetTemplatesAliasesAlias(c, alias)

	res, err := apispec.ParseGetTemplatesAliasesAliasResponse(w.Result())
	require.NoError(t, err)
	require.Equal(t, http.StatusForbidden, res.StatusCode())
}
