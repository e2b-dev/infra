package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/auth/pkg/auth"
	authtypes "github.com/e2b-dev/infra/packages/auth/pkg/types"
	"github.com/e2b-dev/infra/packages/dashboard-api/internal/api"
	authqueries "github.com/e2b-dev/infra/packages/db/pkg/auth/queries"
	"github.com/e2b-dev/infra/packages/db/pkg/testutils"
)

func TestGetTeamsTeamIDLimitsSumsAnActiveAddonForATeamAuthedRequest(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	teamID := testutils.CreateTestTeam(t, db)

	baseline := getTeamLimitsOK(t, db, teamID, &teamID).Limits

	insertHandlerTestAddon(t, db, teamID, time.Now().Add(-time.Hour), nil)

	withAddon := getTeamLimitsOK(t, db, teamID, &teamID).Limits
	require.Equal(t, baseline.MaxLengthHours, withAddon.MaxLengthHours)
	require.Equal(t, baseline.ConcurrentSandboxes+500, withAddon.ConcurrentSandboxes)
	require.Equal(t, baseline.ConcurrentTemplateBuilds+2, withAddon.ConcurrentTemplateBuilds)
	require.Equal(t, baseline.MaxVcpu+8, withAddon.MaxVcpu)
	require.Equal(t, baseline.MaxRamMb+1024, withAddon.MaxRamMb)
	require.Equal(t, baseline.DiskMb+2048, withAddon.DiskMb)
	require.Equal(t, baseline.EventsTtlDays+7, withAddon.EventsTtlDays)
}

func TestGetTeamsTeamIDLimitsReturnsTheRawTier(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	teamID := testutils.CreateTestTeam(t, db)
	require.NoError(t, db.AuthDB.TestsRawSQL(t.Context(),
		`INSERT INTO public.tiers (id, name, disk_mb, concurrent_instances, max_length_hours,
			max_vcpu, max_ram_mb, concurrent_template_builds, events_ttl_days,
			default_free_disk_size_mb, max_disk_size_mb)
		 VALUES ('enterprise_test', 'Enterprise Test', 20480, 100, 24, 8, 8192, 20, 30, 10240, 51200)`,
	))
	require.NoError(t, db.AuthDB.TestsRawSQL(t.Context(),
		`UPDATE public.teams SET tier = 'enterprise_test' WHERE id = $1`,
		teamID,
	))

	require.Equal(t, "enterprise_test", getTeamLimitsOK(t, db, teamID, &teamID).Tier)
}

func TestGetTeamsTeamIDLimitsDoesNotSumAnExpiredAddon(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	teamID := testutils.CreateTestTeam(t, db)

	baseline := getTeamLimitsOK(t, db, teamID, &teamID)

	expiredAt := time.Now().Add(-time.Hour)
	insertHandlerTestAddon(t, db, teamID, time.Now().Add(-2*time.Hour), &expiredAt)

	require.Equal(t, baseline, getTeamLimitsOK(t, db, teamID, &teamID))
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

func insertHandlerTestAddon(t *testing.T, db *testutils.Database, teamID uuid.UUID, validFrom time.Time, validTo *time.Time) {
	t.Helper()

	require.NoError(t, db.AuthDB.TestsRawSQL(t.Context(), `
INSERT INTO public.addons (team_id, name, extra_concurrent_sandboxes, extra_concurrent_template_builds, extra_max_vcpu, extra_max_ram_mb, extra_disk_mb, extra_events_ttl_days, valid_from, valid_to, added_by)
VALUES ($1, 'test addon', 500, 2, 8, 1024, 2048, 7, $2, $3, '00000000-0000-0000-0000-000000000000')
`, teamID, validFrom, validTo))
}
