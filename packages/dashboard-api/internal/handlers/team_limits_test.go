package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/auth/pkg/auth"
	authtypes "github.com/e2b-dev/infra/packages/auth/pkg/types"
	"github.com/e2b-dev/infra/packages/dashboard-api/internal/api"
	authqueries "github.com/e2b-dev/infra/packages/db/pkg/auth/queries"
	"github.com/e2b-dev/infra/packages/db/pkg/testutils"
)

// Neither the team's tier nor an active add-on reaches the limits: they are
// the pushed row, else the deployment's defaults.
func TestGetTeamsTeamIDLimitsReturnsTheDefaultsUntilLimitsArePushed(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	teamID := testutils.CreateTestTeam(t, db)
	require.NoError(t, db.AuthDB.TestsRawSQL(t.Context(), `
INSERT INTO public.addons (team_id, name, extra_concurrent_sandboxes, extra_concurrent_template_builds, extra_max_vcpu, extra_max_ram_mb, extra_disk_mb, extra_events_ttl_days, valid_from, added_by)
VALUES ($1, 'test addon', 500, 2, 8, 1024, 2048, 7, now() - interval '1 hour', '00000000-0000-0000-0000-000000000000')
`, teamID))

	require.NoError(t, db.AuthDB.TestsRawSQL(t.Context(), `
UPDATE public.default_limits
SET max_length_hours = 2, concurrent_sandboxes = 30, concurrent_template_builds = 4,
    max_vcpu = 5, max_ram_mb = 6144, disk_mb = 7168, events_ttl_days = 8
`))
	require.Equal(t, api.UserTeamLimits{
		MaxLengthHours: 2, ConcurrentSandboxes: 30, ConcurrentTemplateBuilds: 4,
		MaxVcpu: 5, MaxRamMb: 6144, DiskMb: 7168, EventsTtlDays: 8,
	}, getTeamLimitsOK(t, db, teamID, &teamID).Limits)

	require.NoError(t, db.AuthDB.TestsRawSQL(t.Context(), `
INSERT INTO public.project_limits (
    team_id, max_length_hours, concurrent_sandboxes, concurrent_template_builds,
    max_vcpu, max_ram_mb, disk_mb, events_ttl_days, default_free_disk_size_mb, max_disk_size_mb,
    api_team_rps_list, api_team_rps_delete, max_secrets
) VALUES ($1, 24, 500, 22, 16, 16384, 20480, 30, 10240, 51200, 0, 0, 10)
`, teamID))
	require.Equal(t, api.UserTeamLimits{
		MaxLengthHours: 24, ConcurrentSandboxes: 500, ConcurrentTemplateBuilds: 22,
		MaxVcpu: 16, MaxRamMb: 16384, DiskMb: 20480, EventsTtlDays: 30,
	}, getTeamLimitsOK(t, db, teamID, &teamID).Limits)
}

func TestGetTeamsTeamIDLimitsRejectsAPathOutsideTheAuthedTeam(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	teamID := testutils.CreateTestTeam(t, db)
	otherTeamID := testutils.CreateTestTeam(t, db)

	response := callGetTeamLimits(t, &APIStore{authDB: db.AuthDB}, teamID, &otherTeamID)
	require.Equal(t, http.StatusForbidden, response.Code, response.Body.String())
}

func TestGetTeamsTeamIDLimitsAllowsAnAdminToReadAnyTeam(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	teamID := testutils.CreateTestTeam(t, db)

	limits := getTeamLimitsOK(t, db, teamID, nil).Limits
	require.Positive(t, limits.ConcurrentSandboxes)
}

func TestGetTeamsTeamIDLimitsReturnsNotFoundForAnUnknownTeam(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)

	response := callGetTeamLimits(t, &APIStore{authDB: db.AuthDB}, uuid.New(), nil)
	require.Equal(t, http.StatusNotFound, response.Code, response.Body.String())
}

func TestGetTeamsTeamIDLimitsRemainsReadableForABlockedTeam(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	teamID := testutils.CreateTestTeam(t, db)
	require.NoError(t, db.AuthDB.TestsRawSQL(t.Context(),
		`UPDATE public.teams SET is_blocked = true, blocked_reason = 'manual review' WHERE id = $1`,
		teamID,
	))

	limits := getTeamLimitsOK(t, db, teamID, &teamID).Limits
	require.Positive(t, limits.ConcurrentSandboxes)
}

func getTeamLimitsOK(t *testing.T, db *testutils.Database, teamID uuid.UUID, authedTeamID *uuid.UUID) api.TeamLimitsResponse {
	t.Helper()

	response := callGetTeamLimits(t, &APIStore{authDB: db.AuthDB}, teamID, authedTeamID)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())

	var body api.TeamLimitsResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))

	return body
}

func callGetTeamLimits(t *testing.T, store *APIStore, teamID uuid.UUID, authedTeamID *uuid.UUID) *httptest.ResponseRecorder {
	t.Helper()

	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	ginCtx.Request = httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/teams/"+teamID.String()+"/limits", nil)
	if authedTeamID != nil {
		auth.SetTeamInfoForTest(t, ginCtx, &authtypes.Team{
			Team: &authqueries.Team{ID: *authedTeamID},
		})
	}

	store.GetTeamsTeamIDLimits(ginCtx, teamID)
	ginCtx.Writer.WriteHeaderNow()

	return recorder
}
