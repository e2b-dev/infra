package aliases

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/db/pkg/testutils"
	"github.com/e2b-dev/infra/packages/db/queries"
)

func TestGetTemplateByAlias_MatchesNamespace(t *testing.T) {
	t.Parallel()
	db := testutils.SetupDatabase(t)
	ctx := t.Context()

	teamID := testutils.CreateTestTeam(t, db)
	templateID := testutils.CreateTestTemplate(t, db, teamID)
	teamSlug := testutils.GetTeamSlug(t, ctx, db, teamID)

	testutils.CreateTestTemplateAliasWithName(t, db, templateID, "my-template", &teamSlug)

	result, err := db.SqlcClient.GetTemplateByAlias(ctx, queries.GetTemplateByAliasParams{
		Alias:     "my-template",
		Namespace: &teamSlug,
	})
	require.NoError(t, err)
	assert.Equal(t, templateID, result.ID)
}

func TestGetTemplateByAlias_MatchesNullNamespace(t *testing.T) {
	t.Parallel()
	db := testutils.SetupDatabase(t)
	ctx := t.Context()

	teamID := testutils.CreateTestTeam(t, db)
	templateID := testutils.CreateTestTemplate(t, db, teamID)

	testutils.CreateTestTemplateAliasWithName(t, db, templateID, "base-template", nil)

	result, err := db.SqlcClient.GetTemplateByAlias(ctx, queries.GetTemplateByAliasParams{
		Alias:     "base-template",
		Namespace: nil,
	})
	require.NoError(t, err)
	assert.Equal(t, templateID, result.ID)
}

func TestGetTemplateByAlias_NotFound(t *testing.T) {
	t.Parallel()
	db := testutils.SetupDatabase(t)
	ctx := t.Context()

	teamID := testutils.CreateTestTeam(t, db)
	templateID := testutils.CreateTestTemplate(t, db, teamID)
	teamSlug := testutils.GetTeamSlug(t, ctx, db, teamID)

	testutils.CreateTestTemplateAliasWithName(t, db, templateID, "team-scoped", &teamSlug)

	// Different namespace should not find it
	otherNamespace := "other-team"
	_, err := db.SqlcClient.GetTemplateByAlias(ctx, queries.GetTemplateByAliasParams{
		Alias:     "team-scoped",
		Namespace: &otherNamespace,
	})
	require.Error(t, err)

	// Non-existent alias should not find it
	_, err = db.SqlcClient.GetTemplateByAlias(ctx, queries.GetTemplateByAliasParams{
		Alias:     "non-existent",
		Namespace: &teamSlug,
	})
	require.Error(t, err)
}

func TestGetTemplateById(t *testing.T) {
	t.Parallel()
	db := testutils.SetupDatabase(t)
	ctx := t.Context()

	teamID := testutils.CreateTestTeam(t, db)
	templateID := testutils.CreateTestTemplate(t, db, teamID)

	result, err := db.SqlcClient.GetTemplateById(ctx, templateID)
	require.NoError(t, err)
	assert.Equal(t, templateID, result.ID)
}
