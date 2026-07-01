package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	ldvalue "github.com/launchdarkly/go-sdk-common/v3/ldvalue"
	"github.com/launchdarkly/go-server-sdk/v7/testhelpers/ldtestdata"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/cfg"
	sharedauth "github.com/e2b-dev/infra/packages/auth/pkg/auth"
	authtypes "github.com/e2b-dev/infra/packages/auth/pkg/types"
	authqueries "github.com/e2b-dev/infra/packages/db/pkg/auth/queries"
	"github.com/e2b-dev/infra/packages/db/pkg/testutils"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
)

// newTestFeatureFlags creates a featureflags.Client with volumes enabled and
// a non-empty default volume type so PostVolumes passes the early checks.
func newTestFeatureFlags(t *testing.T) *featureflags.Client {
	t.Helper()

	td := ldtestdata.DataSource()
	td.Update(td.Flag(featureflags.PersistentVolumesFlag.Key()).VariationForAll(true))
	td.Update(td.Flag(featureflags.DefaultPersistentVolumeType.Key()).ValueForAll(ldvalue.String("local")))

	ff, err := featureflags.NewClientWithDatasource(td)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ff.Close(t.Context()) })

	return ff
}

// newVolumeCreateRequest builds a gin context with a valid POST /volumes JSON body
// and the given team set via auth helper.
func newVolumeCreateRequest(t *testing.T, team *authtypes.Team, volumeName string) (*httptest.ResponseRecorder, *gin.Context) {
	t.Helper()

	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	ginCtx.Request = httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		"/volumes",
		strings.NewReader(`{"name":"`+volumeName+`"}`),
	)
	ginCtx.Request.Header.Set("Content-Type", "application/json")
	sharedauth.SetTeamInfoForTest(t, ginCtx, team)

	return recorder, ginCtx
}

func TestPostVolumes_QuotaExceeded_Returns429(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	teamID := testutils.CreateTestTeam(t, db)

	// Create 2 volumes to reach the limit.
	for i := range 2 {
		_, err := db.SqlcClient.CreateVolume(t.Context(), queries.CreateVolumeParams{
			TeamID:     teamID,
			Name:       fmt.Sprintf("vol-%d", i),
			VolumeType: "local",
		})
		require.NoError(t, err)
	}

	team := &authtypes.Team{
		Team:   &authqueries.Team{ID: teamID},
		Limits: &authtypes.TeamLimits{MaxVolumes: 2},
	}

	recorder, ginCtx := newVolumeCreateRequest(t, team, "vol-c")

	store := &APIStore{
		sqlcDB:       db.SqlcClient,
		featureFlags: newTestFeatureFlags(t),
		config:       cfg.Config{VolumesToken: cfg.VolumesTokenConfig{Enabled: true}},
	}
	store.PostVolumes(ginCtx)

	assert.Equal(t, http.StatusTooManyRequests, recorder.Code)

	var apiErr api.Error
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &apiErr))
	assert.Contains(t, apiErr.Message, "maximum number of volumes")
}

func TestPostVolumes_QuotaNotReached_PassesQuotaCheck(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	teamID := testutils.CreateTestTeam(t, db)

	// Create 1 volume — well under the limit.
	_, err := db.SqlcClient.CreateVolume(t.Context(), queries.CreateVolumeParams{
		TeamID:     teamID,
		Name:       "vol-existing",
		VolumeType: "local",
	})
	require.NoError(t, err)

	clusterID := uuid.New()
	team := &authtypes.Team{
		Team:   &authqueries.Team{ID: teamID, ClusterID: &clusterID},
		Limits: &authtypes.TeamLimits{MaxVolumes: 5},
	}

	recorder, ginCtx := newVolumeCreateRequest(t, team, "vol-new")

	store := &APIStore{
		sqlcDB:       db.SqlcClient,
		featureFlags: newTestFeatureFlags(t),
		config:       cfg.Config{VolumesToken: cfg.VolumesTokenConfig{Enabled: true}},
	}

	// The handler will panic after passing the quota check because there is
	// no orchestrator backend in the test. We recover the panic and only
	// assert that the response was NOT a 429 quota error.
	func() {
		defer func() { recover() }() //nolint:errcheck // intentional panic recovery
		store.PostVolumes(ginCtx)
	}()

	assert.NotEqual(t, http.StatusTooManyRequests, recorder.Code,
		"quota check should pass when under the limit")
}

func TestPostVolumes_UnlimitedQuota_SkipsCheck(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	teamID := testutils.CreateTestTeam(t, db)

	// max_volumes defaults to 0 (unlimited) — no update needed.

	// Create several volumes.
	for i := range 5 {
		_, err := db.SqlcClient.CreateVolume(t.Context(), queries.CreateVolumeParams{
			TeamID:     teamID,
			Name:       fmt.Sprintf("vol-%d", i),
			VolumeType: "local",
		})
		require.NoError(t, err)
	}

	clusterID := uuid.New()
	team := &authtypes.Team{
		Team:   &authqueries.Team{ID: teamID, ClusterID: &clusterID},
		Limits: &authtypes.TeamLimits{MaxVolumes: 0},
	}

	recorder, ginCtx := newVolumeCreateRequest(t, team, "vol-new")

	store := &APIStore{
		sqlcDB:       db.SqlcClient,
		featureFlags: newTestFeatureFlags(t),
		config:       cfg.Config{VolumesToken: cfg.VolumesTokenConfig{Enabled: true}},
	}

	// Same as above — recover the downstream panic and only check quota.
	func() {
		defer func() { recover() }() //nolint:errcheck // intentional panic recovery
		store.PostVolumes(ginCtx)
	}()

	// With max_volumes=0 the quota check is skipped entirely.
	// The handler will fail downstream (no orchestrator), but NOT with 429.
	assert.NotEqual(t, http.StatusTooManyRequests, recorder.Code,
		"unlimited quota (0) should never return 429")
}

func TestCountVolumesByTeamID(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	teamID := testutils.CreateTestTeam(t, db)

	// Initially zero.
	count, err := db.SqlcClient.CountVolumesByTeamID(t.Context(), teamID)
	require.NoError(t, err)
	assert.Equal(t, int64(0), count)

	// Create 3 volumes.
	for i := range 3 {
		_, err := db.SqlcClient.CreateVolume(t.Context(), queries.CreateVolumeParams{
			TeamID:     teamID,
			Name:       fmt.Sprintf("vol-%d", i),
			VolumeType: "local",
		})
		require.NoError(t, err)
	}

	count, err = db.SqlcClient.CountVolumesByTeamID(t.Context(), teamID)
	require.NoError(t, err)
	assert.Equal(t, int64(3), count)

	// Volumes from another team should not be counted.
	otherTeamID := testutils.CreateTestTeam(t, db)
	_, err = db.SqlcClient.CreateVolume(t.Context(), queries.CreateVolumeParams{
		TeamID:     otherTeamID,
		Name:       "other-vol",
		VolumeType: "local",
	})
	require.NoError(t, err)

	count, err = db.SqlcClient.CountVolumesByTeamID(t.Context(), teamID)
	require.NoError(t, err)
	assert.Equal(t, int64(3), count, "should not count volumes from other teams")
}
