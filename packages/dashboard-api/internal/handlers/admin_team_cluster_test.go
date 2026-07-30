package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/dashboard-api/internal/api"
	"github.com/e2b-dev/infra/packages/db/pkg/testutils"
)

func TestPutAdminTeamsTeamIDClusterRegistersAndAssignsAtomically(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	ctx := t.Context()
	teamID := createEnterpriseTestTeam(t, db)

	clusterID := uuid.New()
	authOrgID := "org_test"
	sandboxDomain := "sandbox.example.test"
	request := api.AdminTeamClusterRegistrationRequest{
		ClusterId:                 clusterID,
		ExpectedPreviousClusterId: nil,
		Name:                      "Managed BYOC",
		Endpoint:                  "api.example.test:5008",
		EndpointTls:               true,
		Token:                     "cluster-token",
		SandboxProxyDomain:        &sandboxDomain,
		AuthOrgId:                 &authOrgID,
	}
	authService := &recordingCacheAuthService{}
	store := &APIStore{db: db.SqlcClient, authService: authService}

	first := callClusterRegistration(t, store, teamID, request)
	require.Equal(t, http.StatusOK, first.Code, first.Body.String())

	var firstBody api.AdminTeamClusterRegistrationResponse
	require.NoError(t, json.Unmarshal(first.Body.Bytes(), &firstBody))
	require.True(t, firstBody.Changed)
	require.Equal(t, teamID, firstBody.TeamId)
	require.Equal(t, clusterID, firstBody.ClusterId)
	require.Nil(t, firstBody.PreviousClusterId)
	require.Equal(t, []uuid.UUID{teamID}, authService.invalidated)

	var assignedClusterID uuid.UUID
	var clusterCount int
	require.NoError(t, db.SqlcClient.TestsRawSQLQuery(ctx,
		`SELECT cluster_id FROM public.teams WHERE id = $1`,
		func(rows pgx.Rows) error {
			require.True(t, rows.Next())

			return rows.Scan(&assignedClusterID)
		},
		teamID,
	))
	require.Equal(t, clusterID, assignedClusterID)
	require.NoError(t, db.SqlcClient.TestsRawSQLQuery(ctx,
		`SELECT count(*) FROM public.clusters WHERE id = $1 AND name = $2 AND endpoint = $3 AND token = $4`,
		func(rows pgx.Rows) error {
			require.True(t, rows.Next())

			return rows.Scan(&clusterCount)
		},
		clusterID,
		request.Name,
		request.Endpoint,
		request.Token,
	))
	require.Equal(t, 1, clusterCount)

	retry := callClusterRegistration(t, store, teamID, request)
	require.Equal(t, http.StatusOK, retry.Code, retry.Body.String())

	var retryBody api.AdminTeamClusterRegistrationResponse
	require.NoError(t, json.Unmarshal(retry.Body.Bytes(), &retryBody))
	require.False(t, retryBody.Changed)
	require.Equal(t, []uuid.UUID{teamID, teamID}, authService.invalidated)

	replacementClusterID := uuid.New()
	replacementRequest := request
	replacementRequest.ClusterId = replacementClusterID
	replacementRequest.ExpectedPreviousClusterId = &clusterID
	replacementRequest.Name = "Managed BYOC replacement"
	replacementAuthOrgID := "org_replacement"
	replacementRequest.AuthOrgId = &replacementAuthOrgID

	replacement := callClusterRegistration(t, store, teamID, replacementRequest)
	require.Equal(t, http.StatusOK, replacement.Code, replacement.Body.String())

	var replacementBody api.AdminTeamClusterRegistrationResponse
	require.NoError(t, json.Unmarshal(replacement.Body.Bytes(), &replacementBody))
	require.True(t, replacementBody.Changed)
	require.Equal(t, &clusterID, replacementBody.PreviousClusterId)
	require.Equal(t, replacementClusterID, replacementBody.ClusterId)
	require.Equal(t, []uuid.UUID{teamID, teamID, teamID}, authService.invalidated)

	require.NoError(t, db.SqlcClient.TestsRawSQLQuery(ctx,
		`SELECT cluster_id FROM public.teams WHERE id = $1`,
		func(rows pgx.Rows) error {
			require.True(t, rows.Next())

			return rows.Scan(&assignedClusterID)
		},
		teamID,
	))
	require.Equal(t, replacementClusterID, assignedClusterID)
	require.NoError(t, db.SqlcClient.TestsRawSQLQuery(ctx,
		`SELECT count(*) FROM public.clusters WHERE id IN ($1, $2)`,
		func(rows pgx.Rows) error {
			require.True(t, rows.Next())

			return rows.Scan(&clusterCount)
		},
		clusterID,
		replacementClusterID,
	))
	require.Equal(t, 2, clusterCount, "replacement must preserve the previous immutable cluster row")
}

func TestPutAdminTeamsTeamIDClusterRejectsStaleTeamAndRollsBackCluster(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	ctx := t.Context()
	teamID := createEnterpriseTestTeam(t, db)

	previousClusterID := uuid.New()
	require.NoError(t, db.SqlcClient.TestsRawSQL(ctx,
		`INSERT INTO public.clusters (id, name, endpoint, endpoint_tls, token) VALUES ($1, 'previous', 'previous.test:5008', true, 'previous-token')`,
		previousClusterID,
	))
	require.NoError(t, db.SqlcClient.TestsRawSQL(ctx, "UPDATE public.teams SET cluster_id = $1 WHERE id = $2", previousClusterID, teamID))

	newClusterID := uuid.New()
	request := api.AdminTeamClusterRegistrationRequest{
		ClusterId:                 newClusterID,
		ExpectedPreviousClusterId: nil,
		Name:                      "Managed BYOC",
		Endpoint:                  "api.example.test:5008",
		EndpointTls:               true,
		Token:                     "cluster-token",
	}
	authService := &recordingCacheAuthService{}
	store := &APIStore{db: db.SqlcClient, authService: authService}

	response := callClusterRegistration(t, store, teamID, request)
	require.Equal(t, http.StatusConflict, response.Code, response.Body.String())
	require.Empty(t, authService.invalidated)

	var assignedClusterID uuid.UUID
	var newClusterCount int
	require.NoError(t, db.SqlcClient.TestsRawSQLQuery(ctx,
		`SELECT cluster_id FROM public.teams WHERE id = $1`,
		func(rows pgx.Rows) error {
			require.True(t, rows.Next())

			return rows.Scan(&assignedClusterID)
		},
		teamID,
	))
	require.Equal(t, previousClusterID, assignedClusterID)
	require.NoError(t, db.SqlcClient.TestsRawSQLQuery(ctx,
		`SELECT count(*) FROM public.clusters WHERE id = $1`,
		func(rows pgx.Rows) error {
			require.True(t, rows.Next())

			return rows.Scan(&newClusterCount)
		},
		newClusterID,
	))
	require.Zero(t, newClusterCount)
}

func callClusterRegistration(
	t *testing.T,
	store *APIStore,
	teamID uuid.UUID,
	request api.AdminTeamClusterRegistrationRequest,
) *httptest.ResponseRecorder {
	t.Helper()

	body, err := json.Marshal(request)
	require.NoError(t, err)

	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	ginCtx.Request = httptest.NewRequestWithContext(t.Context(), http.MethodPut, "/admin/teams/"+teamID.String()+"/cluster", bytes.NewReader(body))
	ginCtx.Request.Header.Set("Content-Type", "application/json")

	store.PutAdminTeamsTeamIDCluster(ginCtx, teamID)

	return recorder
}

func createEnterpriseTestTeam(t *testing.T, db *testutils.Database) uuid.UUID {
	t.Helper()

	teamID := uuid.New()
	require.NoError(t, db.SqlcClient.TestsRawSQL(t.Context(), `
		INSERT INTO public.tiers (
			id,
			name,
			disk_mb,
			concurrent_instances,
			max_length_hours,
			max_vcpu,
			max_ram_mb,
			concurrent_template_builds,
			events_ttl_days,
			default_free_disk_size_mb,
			max_disk_size_mb
		)
		VALUES ('enterprise_v1', 'Enterprise tier', 512, 20, 1, 8, 8096, 20, 7, 512, 25512)
	`))
	require.NoError(t, db.SqlcClient.TestsRawSQL(t.Context(),
		`INSERT INTO public.teams (id, name, tier, email, slug) VALUES ($1, $2, 'enterprise_v1', $3, $4)`,
		teamID,
		"Enterprise team",
		"enterprise-"+teamID.String()+"@example.com",
		"enterprise-"+teamID.String()[:8],
	))

	return teamID
}
