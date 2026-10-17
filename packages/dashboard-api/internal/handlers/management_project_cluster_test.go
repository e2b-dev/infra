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

func TestManagementClusterLifecycleReplaysAndEndsAbsent(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	projectID := testutils.CreateTestTeam(t, db)
	clusterID := uuid.New()
	store := &APIStore{db: db.SqlcClient, authService: &recordingCacheAuthService{}}
	registration := managementClusterRegistration()

	require.Equal(t, http.StatusNoContent, callManagementRegisterCluster(t, store, clusterID, registration).Code)
	require.Equal(t, http.StatusNoContent, callManagementRegisterCluster(t, store, clusterID, registration).Code)
	require.Equal(t, http.StatusNoContent, callManagementAssignProjectCluster(t, store, projectID, clusterID).Code)
	require.Equal(t, http.StatusNoContent, callManagementAssignProjectCluster(t, store, projectID, clusterID).Code)
	require.Equal(t, http.StatusNoContent, callManagementDetachProjectCluster(t, store, projectID, clusterID).Code)
	require.Equal(t, http.StatusNoContent, callManagementDetachProjectCluster(t, store, projectID, clusterID).Code)
	require.Equal(t, http.StatusNoContent, callManagementDeleteCluster(t, store, clusterID).Code)
	require.Equal(t, http.StatusNoContent, callManagementDeleteCluster(t, store, clusterID).Code)

	var exists bool
	require.NoError(t, db.SqlcClient.TestsRawSQLQuery(t.Context(),
		`SELECT EXISTS (SELECT FROM public.clusters WHERE id = $1)`,
		func(rows pgx.Rows) error {
			require.True(t, rows.Next())

			return rows.Scan(&exists)
		}, clusterID))
	require.False(t, exists)
}

func TestManagementRegisterClusterRejectsDescriptorChange(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	clusterID := uuid.New()
	store := &APIStore{db: db.SqlcClient}
	registration := managementClusterRegistration()

	require.Equal(t, http.StatusNoContent, callManagementRegisterCluster(t, store, clusterID, registration).Code)
	for name, change := range map[string]func(*api.ManagementClusterRegistrationRequest){
		"name":                      func(r *api.ManagementClusterRegistrationRequest) { r.Name = "different cluster" },
		"endpoint":                  func(r *api.ManagementClusterRegistrationRequest) { r.Endpoint = "changed.example.test:443" },
		"TLS":                       func(r *api.ManagementClusterRegistrationRequest) { r.EndpointTls = !r.EndpointTls },
		"proxy":                     func(r *api.ManagementClusterRegistrationRequest) { r.SandboxProxyDomain = new("changed.example.test") },
		"auth organization":         func(r *api.ManagementClusterRegistrationRequest) { r.AuthOrgId = new("changed-org") },
		"missing auth organization": func(r *api.ManagementClusterRegistrationRequest) { r.AuthOrgId = nil },
		"missing proxy":             func(r *api.ManagementClusterRegistrationRequest) { r.SandboxProxyDomain = nil },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			changed := registration
			changed.Token = "replacement-test-token"
			change(&changed)
			response := callManagementRegisterCluster(t, store, clusterID, changed)
			require.Equal(t, http.StatusConflict, response.Code)
			requireClusterErrorCode(t, response, api.ClusterRegistrationConflict)
		})
	}
}

func TestManagementRegisterClusterRefreshesOnlyToken(t *testing.T) {
	t.Parallel()
	db := testutils.SetupDatabase(t)
	projectID := testutils.CreateTestTeam(t, db)
	clusterID := uuid.New()
	store := &APIStore{db: db.SqlcClient, authService: &recordingCacheAuthService{}}
	registration := managementClusterRegistration()
	registration.AuthOrgId = nil
	require.Equal(t, http.StatusNoContent, callManagementRegisterCluster(t, store, clusterID, registration).Code)
	require.Equal(t, http.StatusNoContent, callManagementAssignProjectCluster(t, store, projectID, clusterID).Code)
	require.NoError(t, db.SqlcClient.TestsRawSQL(t.Context(), `UPDATE public.clusters SET deletion_protection=true WHERE id=$1`, clusterID))
	registration.Token = "replacement-test-token"
	for range 2 {
		require.Equal(t, http.StatusNoContent, callManagementRegisterCluster(t, store, clusterID, registration).Code)
	}
	require.NoError(t, db.SqlcClient.TestsRawSQLQuery(t.Context(),
		`SELECT c.token,c.deletion_protection,t.cluster_id FROM public.clusters c JOIN public.teams t ON t.cluster_id=c.id WHERE c.id=$1 AND t.id=$2`,
		func(rows pgx.Rows) error {
			require.True(t, rows.Next())
			var token string
			var protected bool
			var assigned uuid.UUID
			err := rows.Scan(&token, &protected, &assigned)
			require.Equal(t, registration.Token, token)
			require.True(t, protected)
			require.Equal(t, clusterID, assigned)

			return err
		}, clusterID, projectID))
	// The generic administrator create path still rejects credential changes.
	_, err := store.createCluster(t.Context(), clusterRegistration{
		ClusterID: &clusterID, Name: registration.Name, Endpoint: registration.Endpoint,
		EndpointTLS: registration.EndpointTls, Token: "another-test-token",
		SandboxProxyDomain: registration.SandboxProxyDomain, AuthOrgID: registration.AuthOrgId,
	})
	require.Error(t, err)
}

func TestManagementRegisterClusterAcceptsTheClusterDescriptor(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	registration := managementClusterRegistration()

	require.Equal(t, http.StatusNoContent,
		callManagementRegisterCluster(t, &APIStore{db: db.SqlcClient}, uuid.New(), registration).Code)
}

func TestManagementRegisterClusterRejectsReservedClusterID(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	response := callManagementRegisterCluster(
		t,
		&APIStore{db: db.SqlcClient},
		uuid.Nil,
		managementClusterRegistration(),
	)

	require.Equal(t, http.StatusBadRequest, response.Code)
	requireClusterErrorCode(t, response, api.ClusterRegistrationInvalid)
}

func TestManagementAssignProjectClusterRejectsReservedClusterID(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	projectID := testutils.CreateTestTeam(t, db)
	response := callManagementAssignProjectCluster(
		t,
		&APIStore{db: db.SqlcClient},
		projectID,
		uuid.Nil,
	)

	require.Equal(t, http.StatusBadRequest, response.Code)
	requireClusterErrorCode(t, response, api.ClusterAssignmentInvalid)
}

func TestManagementAssignProjectClusterRejectsDifferentAssignment(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	projectID := testutils.CreateTestTeam(t, db)
	firstID := uuid.New()
	secondID := uuid.New()
	authService := &recordingCacheAuthService{}
	store := &APIStore{db: db.SqlcClient, authService: authService}
	first := managementClusterRegistration()
	second := managementClusterRegistration()
	second.AuthOrgId = new("org_other")

	require.Equal(t, http.StatusNoContent, callManagementRegisterCluster(t, store, firstID, first).Code)
	require.Equal(t, http.StatusNoContent, callManagementRegisterCluster(t, store, secondID, second).Code)
	require.Equal(t, http.StatusNoContent, callManagementAssignProjectCluster(t, store, projectID, firstID).Code)
	response := callManagementAssignProjectCluster(t, store, projectID, secondID)
	require.Equal(t, http.StatusConflict, response.Code, response.Body.String())
	requireClusterErrorCode(t, response, api.ClusterAssignmentAlreadyAssigned)
	require.Equal(t, []uuid.UUID{projectID}, authService.invalidated)
	require.Equal(t, firstID, assignedProjectCluster(t, db, projectID))
}

func TestManagementAssignProjectClusterIgnoresTheProjectTier(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	projectID := testutils.CreateTestTeam(t, db)
	clusterID := uuid.New()
	require.NoError(t, db.SqlcClient.TestsRawSQL(t.Context(),
		`UPDATE public.teams SET tier = 'base_v1' WHERE id = $1`,
		projectID,
	))
	authService := &recordingCacheAuthService{}
	store := &APIStore{db: db.SqlcClient, authService: authService}
	require.Equal(t, http.StatusNoContent,
		callManagementRegisterCluster(t, store, clusterID, managementClusterRegistration()).Code)

	response := callManagementAssignProjectCluster(t, store, projectID, clusterID)
	require.Equal(t, http.StatusNoContent, response.Code, response.Body.String())
	require.Equal(t, []uuid.UUID{projectID}, authService.invalidated)
	require.Equal(t, clusterID, assignedProjectCluster(t, db, projectID))
}

func assignedProjectCluster(t *testing.T, db *testutils.Database, projectID uuid.UUID) uuid.UUID {
	t.Helper()

	var clusterID uuid.UUID
	require.NoError(t, db.SqlcClient.TestsRawSQLQuery(t.Context(),
		`SELECT cluster_id FROM public.teams WHERE id = $1`,
		func(rows pgx.Rows) error {
			require.True(t, rows.Next())

			return rows.Scan(&clusterID)
		},
		projectID,
	))

	return clusterID
}

func managementClusterRegistration() api.ManagementClusterRegistrationRequest {
	return api.ManagementClusterRegistrationRequest{
		Name:               "Managed cluster",
		Endpoint:           "api.example.test:443",
		EndpointTls:        true,
		Token:              "cluster-token",
		SandboxProxyDomain: new("sandbox.example.test"),
		AuthOrgId:          new("org_example"),
	}
}

func callManagementRegisterCluster(
	t *testing.T,
	store *APIStore,
	clusterID uuid.UUID,
	registration api.ManagementClusterRegistrationRequest,
) *httptest.ResponseRecorder {
	t.Helper()

	body, err := json.Marshal(registration)
	require.NoError(t, err)

	return callManagementClusterHandler(t, http.MethodPut, "/v1/management/clusters/"+clusterID.String(), body,
		func(c *gin.Context) { store.ManagementRegisterCluster(c, clusterID) })
}

func callManagementDeleteCluster(t *testing.T, store *APIStore, clusterID uuid.UUID) *httptest.ResponseRecorder {
	t.Helper()

	return callManagementClusterHandler(t, http.MethodDelete, "/v1/management/clusters/"+clusterID.String(), nil,
		func(c *gin.Context) { store.ManagementDeleteCluster(c, clusterID) })
}

func callManagementAssignProjectCluster(
	t *testing.T,
	store *APIStore,
	projectID uuid.UUID,
	clusterID uuid.UUID,
) *httptest.ResponseRecorder {
	t.Helper()

	return callManagementClusterHandler(t, http.MethodPut,
		"/v1/management/projects/"+projectID.String()+"/cluster/"+clusterID.String(), nil,
		func(c *gin.Context) { store.ManagementAssignProjectCluster(c, projectID, clusterID) })
}

func callManagementDetachProjectCluster(
	t *testing.T,
	store *APIStore,
	projectID uuid.UUID,
	clusterID uuid.UUID,
) *httptest.ResponseRecorder {
	t.Helper()

	return callManagementClusterHandler(t, http.MethodDelete,
		"/v1/management/projects/"+projectID.String()+"/cluster/"+clusterID.String(), nil,
		func(c *gin.Context) { store.ManagementDetachProjectCluster(c, projectID, clusterID) })
}

func callManagementClusterHandler(
	t *testing.T,
	method string,
	path string,
	body []byte,
	handler func(*gin.Context),
) *httptest.ResponseRecorder {
	t.Helper()

	recorder := httptest.NewRecorder()
	ginContext, _ := gin.CreateTestContext(recorder)
	ginContext.Request = httptest.NewRequestWithContext(t.Context(), method, path, bytes.NewReader(body))
	ginContext.Request.Header.Set("Content-Type", "application/json")
	handler(ginContext)
	ginContext.Writer.WriteHeaderNow()

	return recorder
}

func requireClusterErrorCode(t *testing.T, response *httptest.ResponseRecorder, code api.ErrorCode) {
	t.Helper()
	var body api.Error
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	require.NotNil(t, body.ErrorCode)
	require.Equal(t, code, *body.ErrorCode)
	require.EqualValues(t, response.Code, body.Code)
}

func TestManagementAssignmentMissingResourcesHaveStableCodes(t *testing.T) {
	t.Parallel()
	db := testutils.SetupDatabase(t)
	projectID := testutils.CreateTestTeam(t, db)
	store := &APIStore{db: db.SqlcClient}
	for _, test := range []struct {
		name      string
		projectID uuid.UUID
		code      api.ErrorCode
	}{
		{"project", uuid.New(), api.ClusterAssignmentProjectNotFound},
		{"cluster", projectID, api.ClusterAssignmentClusterNotFound},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			response := callManagementAssignProjectCluster(t, store, test.projectID, uuid.New())
			require.Equal(t, http.StatusNotFound, response.Code)
			requireClusterErrorCode(t, response, test.code)
		})
	}
}
