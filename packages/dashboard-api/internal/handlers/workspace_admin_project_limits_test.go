package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/db/pkg/testutils"
)

const validLimitsBody = `{
	"concurrent_sandboxes": 40,
	"max_sandbox_length_hours": 12,
	"max_vcpu": 16,
	"max_ram_mb": 32768,
	"disk_mb": 20480,
	"concurrent_template_builds": 30,
	"events_ttl_days": 14,
	"default_free_disk_size_mb": 10240,
	"max_disk_size_mb": 51200
}`

// The push is the only way limits reach this side, and team_limits is what
// gates sandbox creation. Asserting through the view rather than the table
// proves the two are actually wired together.
func TestUpsertProjectLimitsIsVisibleThroughTheView(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	teamID := testutils.CreateTestTeam(t, db)
	store, auth := newLimitsStore(db)

	recorder := callUpsertProjectLimits(t, store, teamID, validLimitsBody)
	require.Equal(t, http.StatusNoContent, recorder.Code, recorder.Body.String())

	limits := readTeamLimits(t, db, teamID)
	require.Equal(t, map[string]int64{
		"max_length_hours": 12, "concurrent_sandboxes": 40, "concurrent_template_builds": 30,
		"max_vcpu": 16, "max_ram_mb": 32768, "disk_mb": 20480, "events_ttl_days": 14,
		"default_free_disk_size_mb": 10240, "max_disk_size_mb": 51200,
	}, limits)

	// Limits are cached with the team, so a push that skips this is invisible
	// until the entry expires.
	require.Equal(t, []uuid.UUID{teamID}, auth.invalidated)
}

// The caller retries, so a redelivered push must land on the same state rather
// than being rejected as a duplicate or applied twice.
func TestUpsertProjectLimitsIsIdempotent(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	teamID := testutils.CreateTestTeam(t, db)
	store, _ := newLimitsStore(db)

	first := callUpsertProjectLimits(t, store, teamID, validLimitsBody)
	require.Equal(t, http.StatusNoContent, first.Code)
	after := readTeamLimits(t, db, teamID)

	second := callUpsertProjectLimits(t, store, teamID, validLimitsBody)
	require.Equal(t, http.StatusNoContent, second.Code)
	require.Equal(t, after, readTeamLimits(t, db, teamID))

	var rows int
	require.NoError(t, db.SqlcClient.TestsRawSQLQuery(t.Context(),
		`SELECT COUNT(*) FROM public.project_limits WHERE team_id = $1`,
		func(r pgx.Rows) error {
			r.Next()

			return r.Scan(&rows)
		}, teamID))
	require.Equal(t, 1, rows)
}

// The team is the row's only foreign key, so a violation means the project is
// unknown here — not that the payload was malformed.
func TestUpsertProjectLimitsRejectsAnUnknownProject(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	store, auth := newLimitsStore(db)

	recorder := callUpsertProjectLimits(t, store, uuid.New(), validLimitsBody)
	require.Equal(t, http.StatusNotFound, recorder.Code)
	require.Empty(t, auth.invalidated, "nothing was written, so nothing should be invalidated")
}

// The free-disk-below-ceiling rule cannot be expressed in the request schema,
// so it is enforced by the column set and has to surface as the caller's error
// rather than ours.
func TestUpsertProjectLimitsRejectsFreeDiskAboveTheCeiling(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	teamID := testutils.CreateTestTeam(t, db)
	store, _ := newLimitsStore(db)

	body := strings.NewReplacer(
		`"default_free_disk_size_mb": 10240`, `"default_free_disk_size_mb": 99999`,
	).Replace(validLimitsBody)

	recorder := callUpsertProjectLimits(t, store, teamID, body)
	require.Equal(t, http.StatusBadRequest, recorder.Code)
}

func newLimitsStore(db *testutils.Database) (*APIStore, *recordingCacheAuthService) {
	auth := &recordingCacheAuthService{}

	return &APIStore{db: db.SqlcClient, authService: auth}, auth
}

func callUpsertProjectLimits(t *testing.T, store *APIStore, teamID uuid.UUID, body string) *httptest.ResponseRecorder {
	t.Helper()

	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	ginCtx.Request = httptest.NewRequestWithContext(t.Context(), http.MethodPut,
		"/v1/management/projects/"+teamID.String()+"/limits", strings.NewReader(body))
	ginCtx.Request.Header.Set("Content-Type", "application/json")

	store.UpsertProjectLimits(ginCtx, teamID)
	// The engine flushes at the end of the handler chain; calling the handler
	// directly skips that, and c.Status alone only records the code.
	ginCtx.Writer.WriteHeaderNow()

	return recorder
}

func readTeamLimits(t *testing.T, db *testutils.Database, teamID uuid.UUID) map[string]int64 {
	t.Helper()

	var raw []byte
	err := db.SqlcClient.TestsRawSQLQuery(t.Context(), `
		SELECT json_build_object(
			'max_length_hours', max_length_hours,
			'concurrent_sandboxes', concurrent_sandboxes,
			'concurrent_template_builds', concurrent_template_builds,
			'max_vcpu', max_vcpu,
			'max_ram_mb', max_ram_mb,
			'disk_mb', disk_mb,
			'events_ttl_days', events_ttl_days,
			'default_free_disk_size_mb', default_free_disk_size_mb,
			'max_disk_size_mb', max_disk_size_mb
		) FROM public.team_limits WHERE id = $1`,
		func(r pgx.Rows) error {
			r.Next()

			return r.Scan(&raw)
		}, teamID)
	require.NoError(t, err)

	out := map[string]int64{}
	require.NoError(t, json.Unmarshal(raw, &out))

	return out
}

type recordingCacheAuthService struct {
	noopAuthService

	invalidated []uuid.UUID
}

func (s *recordingCacheAuthService) InvalidateTeamCache(_ context.Context, teamID uuid.UUID) error {
	s.invalidated = append(s.invalidated, teamID)

	return nil
}
