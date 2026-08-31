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
	projectID := createClusterAssignmentTestTeam(t, db)
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
	registration.Endpoint = "changed.example.test:443"
	require.Equal(t, http.StatusConflict, callManagementRegisterCluster(t, store, clusterID, registration).Code)
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
}

func TestManagementAssignProjectClusterRejectsReservedClusterID(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	projectID := createClusterAssignmentTestTeam(t, db)
	response := callManagementAssignProjectCluster(
		t,
		&APIStore{db: db.SqlcClient},
		projectID,
		uuid.Nil,
	)

	require.Equal(t, http.StatusBadRequest, response.Code)
}

func TestManagementAssignProjectClusterRejectsDifferentAssignment(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	projectID := createClusterAssignmentTestTeam(t, db)
	firstID := uuid.New()
	secondID := uuid.New()
	store := &APIStore{db: db.SqlcClient, authService: &recordingCacheAuthService{}}
	first := managementClusterRegistration()
	second := managementClusterRegistration()
	second.AuthOrgId = new("org_other")

	require.Equal(t, http.StatusNoContent, callManagementRegisterCluster(t, store, firstID, first).Code)
	require.Equal(t, http.StatusNoContent, callManagementRegisterCluster(t, store, secondID, second).Code)
	require.Equal(t, http.StatusNoContent, callManagementAssignProjectCluster(t, store, projectID, firstID).Code)
	require.Equal(t, http.StatusConflict, callManagementAssignProjectCluster(t, store, projectID, secondID).Code)
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
