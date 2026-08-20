package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/dashboard-api/internal/api"
	dashboardqueries "github.com/e2b-dev/infra/packages/db/pkg/dashboard/queries"
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

func TestDeleteAdminClustersClusterIDDeletesIdempotently(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	ctx := t.Context()
	clusterID := uuid.New()
	require.NoError(t, db.SqlcClient.TestsRawSQL(ctx,
		`INSERT INTO public.clusters (id, name, endpoint, endpoint_tls, token) VALUES ($1, 'managed', 'api.example.test:5008', true, 'token')`,
		clusterID,
	))

	store := &APIStore{db: db.SqlcClient}
	response := callDeleteCluster(t, store, clusterID)
	require.Equal(t, http.StatusNoContent, response.Code, response.Body.String())

	replayed := callDeleteCluster(t, store, clusterID)
	require.Equal(t, http.StatusNoContent, replayed.Code, replayed.Body.String())

	var count int
	require.NoError(t, db.SqlcClient.TestsRawSQLQuery(ctx,
		`SELECT count(*) FROM public.clusters WHERE id = $1`,
		func(rows pgx.Rows) error {
			require.True(t, rows.Next())

			return rows.Scan(&count)
		},
		clusterID,
	))
	require.Zero(t, count)
}

func TestDeleteAdminClustersClusterIDWaitsForConcurrentTeamReference(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	ctx := t.Context()
	teamID := createClusterAssignmentTestTeam(t, db)
	clusterID := uuid.New()
	require.NoError(t, db.SqlcClient.TestsRawSQL(ctx,
		`INSERT INTO public.clusters (id, name, endpoint, endpoint_tls, token) VALUES ($1, 'managed', 'api.example.test:5008', true, 'token')`,
		clusterID,
	))

	referenceClient, referenceTx, err := db.SqlcClient.WithTx(ctx)
	require.NoError(t, err)
	defer func() {
		_ = referenceTx.Rollback(t.Context())
	}()

	updated, err := referenceClient.Dashboard.AssignTeamCluster(ctx, dashboardqueries.AssignTeamClusterParams{
		TeamID:    teamID,
		ClusterID: clusterID,
	})
	require.NoError(t, err)
	require.EqualValues(t, 1, updated)

	store := &APIStore{db: db.SqlcClient}
	responseCh := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		responseCh <- callDeleteCluster(t, store, clusterID)
	}()

	require.Eventually(t, func() bool {
		var blocked bool
		err := db.SqlcClient.TestsRawSQLQuery(ctx, `
			SELECT EXISTS (
				SELECT 1
				FROM pg_stat_activity
				WHERE datname = current_database()
				  AND state = 'active'
				  AND wait_event_type = 'Lock'
				  AND query LIKE '%DELETE FROM public.clusters%'
			)`, func(rows pgx.Rows) error {
			require.True(t, rows.Next())

			return rows.Scan(&blocked)
		})

		return err == nil && blocked
	}, 5*time.Second, 10*time.Millisecond)

	select {
	case response := <-responseCh:
		require.FailNow(t, "cluster deletion returned before the team reference committed", response.Body.String())
	default:
	}

	require.NoError(t, referenceTx.Commit(ctx))

	select {
	case response := <-responseCh:
		require.Equal(t, http.StatusConflict, response.Code, response.Body.String())
	case <-time.After(5 * time.Second):
		require.FailNow(t, "cluster deletion did not finish after the team reference committed")
	}

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

func TestDeleteAdminClustersClusterIDRejectsEnvironmentHistoryReference(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	ctx := t.Context()
	teamID := createClusterAssignmentTestTeam(t, db)
	clusterID := uuid.New()
	require.NoError(t, db.SqlcClient.TestsRawSQL(ctx,
		`INSERT INTO public.clusters (id, name, endpoint, endpoint_tls, token) VALUES ($1, 'managed', 'api.example.test:5008', true, 'token')`,
		clusterID,
	))
	require.NoError(t, db.SqlcClient.TestsRawSQL(ctx,
		`INSERT INTO public.envs (id, team_id, public, updated_at, source, cluster_id) VALUES ($1, $2, false, NOW(), 'test', $3)`,
		"cluster-history-"+uuid.NewString(),
		teamID,
		clusterID,
	))

	store := &APIStore{db: db.SqlcClient}
	response := callDeleteCluster(t, store, clusterID)
	require.Equal(t, http.StatusConflict, response.Code, response.Body.String())

	var count int
	require.NoError(t, db.SqlcClient.TestsRawSQLQuery(ctx,
		`SELECT count(*) FROM public.clusters WHERE id = $1`,
		func(rows pgx.Rows) error {
			require.True(t, rows.Next())

			return rows.Scan(&count)
		},
		clusterID,
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

func TestDeleteAdminTeamsTeamIDClusterClusterIDDetachesIdempotently(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	ctx := t.Context()
	teamID := createClusterAssignmentTestTeam(t, db)
	clusterID := uuid.New()
	require.NoError(t, db.SqlcClient.TestsRawSQL(ctx,
		`INSERT INTO public.clusters (id, name, endpoint, endpoint_tls, token) VALUES ($1, 'managed', 'api.example.test:5008', true, 'token')`,
		clusterID,
	))
	require.NoError(t, db.SqlcClient.TestsRawSQL(ctx,
		`UPDATE public.teams SET cluster_id = $1 WHERE id = $2`,
		clusterID,
		teamID,
	))

	authService := &recordingCacheAuthService{}
	store := &APIStore{db: db.SqlcClient, authService: authService}
	response := callDetachCluster(t, store, teamID, clusterID)
	require.Equal(t, http.StatusNoContent, response.Code, response.Body.String())

	replayed := callDetachCluster(t, store, teamID, clusterID)
	require.Equal(t, http.StatusNoContent, replayed.Code, replayed.Body.String())
	require.Equal(t, []uuid.UUID{teamID, teamID}, authService.invalidated)

	var assignedClusterID *uuid.UUID
	require.NoError(t, db.SqlcClient.TestsRawSQLQuery(ctx,
		`SELECT cluster_id FROM public.teams WHERE id = $1`,
		func(rows pgx.Rows) error {
			require.True(t, rows.Next())

			return rows.Scan(&assignedClusterID)
		},
		teamID,
	))
	require.Nil(t, assignedClusterID)
}

func TestDeleteAdminTeamsTeamIDClusterClusterIDRetriesCacheInvalidation(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	ctx := t.Context()
	teamID := createClusterAssignmentTestTeam(t, db)
	clusterID := uuid.New()
	require.NoError(t, db.SqlcClient.TestsRawSQL(ctx,
		`INSERT INTO public.clusters (id, name, endpoint, endpoint_tls, token) VALUES ($1, 'managed', 'api.example.test:5008', true, 'token')`,
		clusterID,
	))
	require.NoError(t, db.SqlcClient.TestsRawSQL(ctx,
		`UPDATE public.teams SET cluster_id = $1 WHERE id = $2`,
		clusterID,
		teamID,
	))

	authService := &configurableCacheAuthService{invalidateErr: errors.New("cache unavailable")}
	store := &APIStore{db: db.SqlcClient, authService: authService}
	failed := callDetachCluster(t, store, teamID, clusterID)
	require.Equal(t, http.StatusInternalServerError, failed.Code, failed.Body.String())

	var assignedClusterID *uuid.UUID
	require.NoError(t, db.SqlcClient.TestsRawSQLQuery(ctx,
		`SELECT cluster_id FROM public.teams WHERE id = $1`,
		func(rows pgx.Rows) error {
			require.True(t, rows.Next())

			return rows.Scan(&assignedClusterID)
		},
		teamID,
	))
	require.Nil(t, assignedClusterID)

	failedReplay := callDetachCluster(t, store, teamID, clusterID)
	require.Equal(t, http.StatusInternalServerError, failedReplay.Code, failedReplay.Body.String())

	authService.invalidateErr = nil
	replayed := callDetachCluster(t, store, teamID, clusterID)
	require.Equal(t, http.StatusNoContent, replayed.Code, replayed.Body.String())
	require.Equal(t, []uuid.UUID{teamID, teamID, teamID}, authService.invalidated)
}

func TestDeleteAdminTeamsTeamIDClusterClusterIDPreservesReplacement(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	ctx := t.Context()
	teamID := createClusterAssignmentTestTeam(t, db)
	staleClusterID := uuid.New()
	replacementClusterID := uuid.New()
	require.NoError(t, db.SqlcClient.TestsRawSQL(ctx,
		`INSERT INTO public.clusters (id, name, endpoint, endpoint_tls, token) VALUES ($1, 'replacement', 'replacement.example.test:5008', true, 'token')`,
		replacementClusterID,
	))
	require.NoError(t, db.SqlcClient.TestsRawSQL(ctx,
		`UPDATE public.teams SET cluster_id = $1 WHERE id = $2`,
		replacementClusterID,
		teamID,
	))

	authService := &recordingCacheAuthService{}
	store := &APIStore{db: db.SqlcClient, authService: authService}
	response := callDetachCluster(t, store, teamID, staleClusterID)
	require.Equal(t, http.StatusConflict, response.Code, response.Body.String())
	require.Empty(t, authService.invalidated)

	var assignedClusterID uuid.UUID
	require.NoError(t, db.SqlcClient.TestsRawSQLQuery(ctx,
		`SELECT cluster_id FROM public.teams WHERE id = $1`,
		func(rows pgx.Rows) error {
			require.True(t, rows.Next())

			return rows.Scan(&assignedClusterID)
		},
		teamID,
	))
	require.Equal(t, replacementClusterID, assignedClusterID)

	missingTeam := callDetachCluster(t, store, uuid.New(), staleClusterID)
	require.Equal(t, http.StatusNotFound, missingTeam.Code, missingTeam.Body.String())
}

func TestDeleteAdminTeamsTeamIDClusterClusterIDWaitsForReplacement(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	ctx := t.Context()
	teamID := createClusterAssignmentTestTeam(t, db)
	staleClusterID := uuid.New()
	replacementClusterID := uuid.New()
	require.NoError(t, db.SqlcClient.TestsRawSQL(ctx,
		`INSERT INTO public.clusters (id, name, endpoint, endpoint_tls, token) VALUES ($1, 'stale', 'stale.example.test:5008', true, 'token'), ($2, 'replacement', 'replacement.example.test:5008', true, 'token')`,
		staleClusterID,
		replacementClusterID,
	))
	require.NoError(t, db.SqlcClient.TestsRawSQL(ctx,
		`UPDATE public.teams SET cluster_id = $1 WHERE id = $2`,
		staleClusterID,
		teamID,
	))

	replacementClient, replacementTx, err := db.SqlcClient.WithTx(ctx)
	require.NoError(t, err)
	defer func() {
		_ = replacementTx.Rollback(t.Context())
	}()

	updated, err := replacementClient.Dashboard.AssignTeamCluster(ctx, dashboardqueries.AssignTeamClusterParams{
		TeamID:    teamID,
		ClusterID: replacementClusterID,
	})
	require.NoError(t, err)
	require.EqualValues(t, 1, updated)

	store := &APIStore{db: db.SqlcClient, authService: &recordingCacheAuthService{}}
	responseCh := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		responseCh <- callDetachCluster(t, store, teamID, staleClusterID)
	}()

	require.Eventually(t, func() bool {
		var blocked bool
		err := db.SqlcClient.TestsRawSQLQuery(ctx, `
			SELECT EXISTS (
				SELECT 1
				FROM pg_stat_activity
				WHERE datname = current_database()
				  AND state = 'active'
				  AND wait_event_type = 'Lock'
				  AND query LIKE '%WITH locked_team AS MATERIALIZED (%'
			)`, func(rows pgx.Rows) error {
			require.True(t, rows.Next())

			return rows.Scan(&blocked)
		})

		return err == nil && blocked
	}, 5*time.Second, 10*time.Millisecond)

	select {
	case response := <-responseCh:
		require.FailNow(t, "detach returned before replacement committed", response.Body.String())
	default:
	}

	require.NoError(t, replacementTx.Commit(ctx))

	select {
	case response := <-responseCh:
		require.Equal(t, http.StatusConflict, response.Code, response.Body.String())
	case <-time.After(5 * time.Second):
		require.FailNow(t, "detach did not finish after replacement committed")
	}

	var assignedClusterID uuid.UUID
	require.NoError(t, db.SqlcClient.TestsRawSQLQuery(ctx,
		`SELECT cluster_id FROM public.teams WHERE id = $1`,
		func(rows pgx.Rows) error {
			require.True(t, rows.Next())

			return rows.Scan(&assignedClusterID)
		},
		teamID,
	))
	require.Equal(t, replacementClusterID, assignedClusterID)
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

func callDeleteCluster(t *testing.T, store *APIStore, clusterID uuid.UUID) *httptest.ResponseRecorder {
	t.Helper()

	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	ginCtx.Request = httptest.NewRequestWithContext(
		t.Context(),
		http.MethodDelete,
		"/admin/clusters/"+clusterID.String(),
		nil,
	)
	store.DeleteAdminClustersClusterID(ginCtx, clusterID)
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

func callDetachCluster(
	t *testing.T,
	store *APIStore,
	teamID uuid.UUID,
	clusterID uuid.UUID,
) *httptest.ResponseRecorder {
	t.Helper()

	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	ginCtx.Request = httptest.NewRequestWithContext(
		t.Context(),
		http.MethodDelete,
		"/admin/teams/"+teamID.String()+"/cluster/"+clusterID.String(),
		nil,
	)
	store.DeleteAdminTeamsTeamIDClusterClusterID(ginCtx, teamID, clusterID)
	ginCtx.Writer.WriteHeaderNow()

	return recorder
}

type configurableCacheAuthService struct {
	noopAuthService

	invalidated   []uuid.UUID
	invalidateErr error
}

func (s *configurableCacheAuthService) InvalidateTeamCache(_ context.Context, teamID uuid.UUID) error {
	s.invalidated = append(s.invalidated, teamID)

	return s.invalidateErr
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
