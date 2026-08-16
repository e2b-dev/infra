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

func TestPostAdminClustersCreatesImmutableCluster(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	ctx := t.Context()
	authOrgID := "org_test"
	sandboxDomain := "sandbox.example.test"
	request := api.AdminClusterCreateRequest{
		Name:               "Managed BYOC",
		Endpoint:           "api.example.test:5008",
		EndpointTls:        true,
		Token:              "cluster-token",
		SandboxProxyDomain: &sandboxDomain,
		AuthOrgId:          &authOrgID,
	}
	store := &APIStore{db: db.SqlcClient}

	response := callCreateCluster(t, store, request)
	require.Equal(t, http.StatusCreated, response.Code, response.Body.String())

	var created api.AdminClusterCreateResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &created))
	require.NotEqual(t, uuid.Nil, created.ClusterId)

	var count int
	require.NoError(t, db.SqlcClient.TestsRawSQLQuery(ctx,
		`SELECT count(*) FROM public.clusters WHERE id = $1 AND name = $2 AND endpoint = $3 AND token = $4`,
		func(rows pgx.Rows) error {
			require.True(t, rows.Next())

			return rows.Scan(&count)
		},
		created.ClusterId,
		request.Name,
		request.Endpoint,
		request.Token,
	))
	require.Equal(t, 1, count)

	conflict := callCreateCluster(t, store, request)
	require.Equal(t, http.StatusConflict, conflict.Code, conflict.Body.String())
	require.NoError(t, db.SqlcClient.TestsRawSQLQuery(ctx,
		`SELECT count(*) FROM public.clusters WHERE id = $1 AND name = $2`,
		func(rows pgx.Rows) error {
			require.True(t, rows.Next())

			return rows.Scan(&count)
		},
		created.ClusterId,
		request.Name,
	))
	require.Equal(t, 1, count)
}

func TestPutAdminTeamsTeamIDClusterAssignsExistingCluster(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	ctx := t.Context()
	teamID := createClusterAssignmentTestTeam(t, db)
	clusterID := uuid.New()
	require.NoError(t, db.SqlcClient.TestsRawSQL(ctx,
		`INSERT INTO public.clusters (id, name, endpoint, endpoint_tls, token) VALUES ($1, 'managed', 'api.example.test:5008', true, 'token')`,
		clusterID,
	))

	authService := &recordingCacheAuthService{}
	store := &APIStore{db: db.SqlcClient, authService: authService}
	response := callAssignCluster(t, store, teamID, api.AdminTeamClusterAssignmentRequest{ClusterId: clusterID})
	require.Equal(t, http.StatusNoContent, response.Code, response.Body.String())
	require.Equal(t, []uuid.UUID{teamID}, authService.invalidated)

	var assignedClusterID uuid.UUID
	require.NoError(t, db.SqlcClient.TestsRawSQLQuery(ctx,
		`SELECT cluster_id FROM public.teams WHERE id = $1`,
		func(rows pgx.Rows) error {
			require.True(t, rows.Next())

			return rows.Scan(&assignedClusterID)
		},
		teamID,
	))
	require.Equal(t, clusterID, assignedClusterID)
}

func TestGetAdminTeamsTeamIDClusterReturnsOnlyAssignment(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	teamID := createClusterAssignmentTestTeam(t, db)
	clusterID := uuid.New()
	require.NoError(t, db.SqlcClient.TestsRawSQL(t.Context(),
		`INSERT INTO public.clusters (id, name, endpoint, endpoint_tls, token) VALUES ($1, 'managed', 'api.example.test:5008', true, 'secret-token')`,
		clusterID,
	))
	require.NoError(t, db.SqlcClient.TestsRawSQL(t.Context(),
		`UPDATE public.teams SET cluster_id = $1 WHERE id = $2`,
		clusterID,
		teamID,
	))

	store := &APIStore{db: db.SqlcClient}
	response := callGetClusterAssignment(t, store, teamID)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var assignment api.AdminTeamClusterAssignmentResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &assignment))
	require.Equal(t, clusterID, assignment.ClusterId)
	require.NotContains(t, response.Body.String(), "secret-token")

	missing := callGetClusterAssignment(t, store, uuid.New())
	require.Equal(t, http.StatusNotFound, missing.Code, missing.Body.String())
}

func TestPutAdminTeamsTeamIDClusterRejectsMissingResources(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	teamID := createClusterAssignmentTestTeam(t, db)
	store := &APIStore{db: db.SqlcClient, authService: &recordingCacheAuthService{}}

	missingCluster := callAssignCluster(t, store, teamID, api.AdminTeamClusterAssignmentRequest{ClusterId: uuid.New()})
	require.Equal(t, http.StatusNotFound, missingCluster.Code, missingCluster.Body.String())

	clusterID := uuid.New()
	require.NoError(t, db.SqlcClient.TestsRawSQL(t.Context(),
		`INSERT INTO public.clusters (id, name, endpoint, endpoint_tls, token) VALUES ($1, 'managed', 'api.example.test:5008', true, 'token')`,
		clusterID,
	))
	missingTeam := callAssignCluster(t, store, uuid.New(), api.AdminTeamClusterAssignmentRequest{ClusterId: clusterID})
	require.Equal(t, http.StatusNotFound, missingTeam.Code, missingTeam.Body.String())
}

func callCreateCluster(t *testing.T, store *APIStore, request api.AdminClusterCreateRequest) *httptest.ResponseRecorder {
	t.Helper()

	body, err := json.Marshal(request)
	require.NoError(t, err)

	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	ginCtx.Request = httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/admin/clusters", bytes.NewReader(body))
	ginCtx.Request.Header.Set("Content-Type", "application/json")
	store.PostAdminClusters(ginCtx)
	ginCtx.Writer.WriteHeaderNow()

	return recorder
}

func callAssignCluster(
	t *testing.T,
	store *APIStore,
	teamID uuid.UUID,
	request api.AdminTeamClusterAssignmentRequest,
) *httptest.ResponseRecorder {
	t.Helper()

	body, err := json.Marshal(request)
	require.NoError(t, err)

	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	ginCtx.Request = httptest.NewRequestWithContext(t.Context(), http.MethodPut, "/admin/teams/"+teamID.String()+"/cluster", bytes.NewReader(body))
	ginCtx.Request.Header.Set("Content-Type", "application/json")
	store.PutAdminTeamsTeamIDCluster(ginCtx, teamID)
	ginCtx.Writer.WriteHeaderNow()

	return recorder
}

func callGetClusterAssignment(
	t *testing.T,
	store *APIStore,
	teamID uuid.UUID,
) *httptest.ResponseRecorder {
	t.Helper()

	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	ginCtx.Request = httptest.NewRequestWithContext(
		t.Context(),
		http.MethodGet,
		"/admin/teams/"+teamID.String()+"/cluster",
		nil,
	)
	store.GetAdminTeamsTeamIDCluster(ginCtx, teamID)
	ginCtx.Writer.WriteHeaderNow()

	return recorder
}

func createClusterAssignmentTestTeam(t *testing.T, db *testutils.Database) uuid.UUID {
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
		VALUES ('cluster_assignment_test', 'Cluster assignment test', 512, 20, 1, 8, 8096, 20, 7, 512, 25512)
	`))
	require.NoError(t, db.SqlcClient.TestsRawSQL(t.Context(),
		`INSERT INTO public.teams (id, name, tier, email, slug) VALUES ($1, $2, 'cluster_assignment_test', $3, $4)`,
		teamID,
		"Cluster assignment test team",
		"cluster-"+teamID.String()+"@example.com",
		"cluster-"+teamID.String()[:8],
	))

	return teamID
}
