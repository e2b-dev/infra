package templates

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/db/pkg/testutils"
	"github.com/e2b-dev/infra/packages/db/queries"
)

// firstPageCursor returns a cursor that selects the first page of a descending
// listing (newer than any real row).
func firstPageCursor() (time.Time, string) {
	return time.Now().Add(100 * 365 * 24 * time.Hour), ""
}

func TestGetTeamTemplatesWithCursor_OrdersDescAndPaginates(t *testing.T) {
	t.Parallel()
	db := testutils.SetupDatabase(t)
	ctx := t.Context()

	teamID := testutils.CreateTestTeam(t, db)

	// Three templates with distinct, increasing created_at (index 2 is newest).
	templateIDs := make([]string, 3)
	for i := range templateIDs {
		templateIDs[i] = testutils.CreateTestTemplate(t, db, teamID)
		err := db.SqlcClient.TestsRawSQL(ctx,
			"UPDATE public.envs SET created_at = NOW() - ($2 || ' hours')::interval WHERE id = $1",
			templateIDs[i], 3-i,
		)
		require.NoError(t, err)
	}

	cursorTime, cursorID := firstPageCursor()
	rows, err := db.SqlcClient.GetTeamTemplatesWithCursor(ctx, queries.GetTeamTemplatesWithCursorParams{
		TeamID:          teamID,
		IncludeDefaults: false,
		CursorCreatedAt: cursorTime,
		CursorID:        cursorID,
		LimitPlusOne:    10,
	})
	require.NoError(t, err)
	require.Len(t, rows, 3)
	// Newest first.
	require.Equal(t, []string{templateIDs[2], templateIDs[1], templateIDs[0]},
		[]string{rows[0].TemplateID, rows[1].TemplateID, rows[2].TemplateID})

	// Keyset pagination: page of 2 (request 3 = limit+1 to detect more), then
	// continue from the last returned row's cursor.
	firstPage, err := db.SqlcClient.GetTeamTemplatesWithCursor(ctx, queries.GetTeamTemplatesWithCursorParams{
		TeamID:          teamID,
		IncludeDefaults: false,
		CursorCreatedAt: cursorTime,
		CursorID:        cursorID,
		LimitPlusOne:    3,
	})
	require.NoError(t, err)
	require.Len(t, firstPage, 3) // 2 + 1 sentinel
	last := firstPage[1]         // the 2nd item is the page boundary

	secondPage, err := db.SqlcClient.GetTeamTemplatesWithCursor(ctx, queries.GetTeamTemplatesWithCursorParams{
		TeamID:          teamID,
		IncludeDefaults: false,
		CursorCreatedAt: last.CreatedAt,
		CursorID:        last.TemplateID,
		LimitPlusOne:    10,
	})
	require.NoError(t, err)
	require.Len(t, secondPage, 1)
	require.Equal(t, templateIDs[0], secondPage[0].TemplateID)
}

func TestGetTeamTemplatesWithCursor_IncludeDefaults(t *testing.T) {
	t.Parallel()
	db := testutils.SetupDatabase(t)
	ctx := t.Context()

	teamID := testutils.CreateTestTeam(t, db)
	ownTemplate := testutils.CreateTestTemplate(t, db, teamID)

	// A default template owned by a different team — only reachable via the
	// defaults branch.
	otherTeamID := testutils.CreateTestTeam(t, db)
	defaultTemplate := testutils.CreateTestTemplate(t, db, otherTeamID)
	err := db.SqlcClient.TestsRawSQL(ctx,
		"INSERT INTO public.env_defaults (env_id, description) VALUES ($1, $2)",
		defaultTemplate, "a default template",
	)
	require.NoError(t, err)

	cursorTime, cursorID := firstPageCursor()

	// Without defaults: only the team's own template.
	withoutDefaults, err := db.SqlcClient.GetTeamTemplatesWithCursor(ctx, queries.GetTeamTemplatesWithCursorParams{
		TeamID:          teamID,
		IncludeDefaults: false,
		CursorCreatedAt: cursorTime,
		CursorID:        cursorID,
		LimitPlusOne:    10,
	})
	require.NoError(t, err)
	require.Len(t, withoutDefaults, 1)
	require.Equal(t, ownTemplate, withoutDefaults[0].TemplateID)
	require.False(t, withoutDefaults[0].IsDefault)

	// With defaults: own template + the default one, flagged and described.
	withDefaults, err := db.SqlcClient.GetTeamTemplatesWithCursor(ctx, queries.GetTeamTemplatesWithCursorParams{
		TeamID:          teamID,
		IncludeDefaults: true,
		CursorCreatedAt: cursorTime,
		CursorID:        cursorID,
		LimitPlusOne:    10,
	})
	require.NoError(t, err)

	byID := make(map[string]queries.GetTeamTemplatesWithCursorRow, len(withDefaults))
	for _, r := range withDefaults {
		byID[r.TemplateID] = r
	}
	require.Contains(t, byID, ownTemplate)
	require.Contains(t, byID, defaultTemplate)
	require.False(t, byID[ownTemplate].IsDefault)
	require.True(t, byID[defaultTemplate].IsDefault)
	require.NotNil(t, byID[defaultTemplate].DefaultDescription)
	require.Equal(t, "a default template", *byID[defaultTemplate].DefaultDescription)
}
