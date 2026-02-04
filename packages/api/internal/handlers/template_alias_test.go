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
	templatecache "github.com/e2b-dev/infra/packages/api/internal/cache/templates"
	"github.com/e2b-dev/infra/packages/api/internal/db/types"
	authqueries "github.com/e2b-dev/infra/packages/db/pkg/auth/queries"
	"github.com/e2b-dev/infra/packages/db/pkg/testutils"
)

func TestQueryNotExistingTemplateAlias(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)
	ctx := t.Context()

	store := &APIStore{
		sqlcDB:        testDB.SqlcClient,
		authDB:        testDB.AuthDb,
		templateCache: templatecache.NewTemplateCache(testDB.SqlcClient),
	}

	alias := "non-existing-template-alias"
	teamID := testutils.CreateTestTeam(t, testDB)
	teamSlug := testutils.GetTeamSlug(t, ctx, testDB, teamID)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/templates/aliases/%s", alias), nil)
	c.Set(
		auth.TeamContextKey,
		&types.Team{
			Team: &authqueries.Team{
				ID:   teamID,
				Slug: teamSlug,
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
	ctx := t.Context()

	teamID := testutils.CreateTestTeam(t, testDB)
	teamSlug := testutils.GetTeamSlug(t, ctx, testDB, teamID)
	templateID := testutils.CreateTestTemplate(t, testDB, teamID)

	// Create alias with team's namespace
	alias := testutils.CreateTestTemplateAliasWithNamespace(t, testDB, templateID, &teamSlug)

	store := &APIStore{
		sqlcDB:        testDB.SqlcClient,
		authDB:        testDB.AuthDb,
		templateCache: templatecache.NewTemplateCache(testDB.SqlcClient),
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/templates/aliases/%s", alias), nil)
	c.Set(
		auth.TeamContextKey,
		&types.Team{
			Team: &authqueries.Team{
				ID:   teamID,
				Slug: teamSlug,
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
	ctx := t.Context()

	ownerTeamID := testutils.CreateTestTeam(t, testDB)
	ownerTeamSlug := testutils.GetTeamSlug(t, ctx, testDB, ownerTeamID)
	foreignTeamID := testutils.CreateTestTeam(t, testDB)
	foreignTeamSlug := testutils.GetTeamSlug(t, ctx, testDB, foreignTeamID)

	templateID := testutils.CreateTestTemplate(t, testDB, ownerTeamID)
	// Create alias with owner's namespace
	alias := testutils.CreateTestTemplateAliasWithNamespace(t, testDB, templateID, &ownerTeamSlug)

	store := &APIStore{
		sqlcDB:        testDB.SqlcClient,
		authDB:        testDB.AuthDb,
		templateCache: templatecache.NewTemplateCache(testDB.SqlcClient),
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/templates/aliases/%s", alias), nil)
	c.Set(
		auth.TeamContextKey,
		&types.Team{
			Team: &authqueries.Team{
				ID:   foreignTeamID,
				Slug: foreignTeamSlug,
			},
		},
	)

	store.GetTemplatesAliasesAlias(c, alias)

	res, err := apispec.ParseGetTemplatesAliasesAliasResponse(w.Result())
	require.NoError(t, err)
	// Foreign team uses their own namespace for lookup, which won't match the owner's namespace
	// This results in 404 (not found) instead of 403 (forbidden) with the new exact match behavior
	require.Equal(t, http.StatusNotFound, res.StatusCode())
}
