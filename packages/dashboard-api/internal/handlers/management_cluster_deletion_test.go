package handlers

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/dashboard-api/internal/api"
	"github.com/e2b-dev/infra/packages/db/pkg/dberrors"
	"github.com/e2b-dev/infra/packages/db/pkg/testutils"
	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
)

func callManagementRequestClusterDeletion(t *testing.T, store *APIStore, clusterID uuid.UUID) int {
	t.Helper()

	return callManagementClusterHandler(t, http.MethodPut,
		"/v1/management/clusters/"+clusterID.String()+"/deletion", nil,
		func(c *gin.Context) { store.ManagementRequestClusterDeletion(c, clusterID) }).Code
}

func TestManagementClusterDeletionRequestKeepsProtectionOff(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	store := &APIStore{db: db.SqlcClient}
	ctx := t.Context()
	conn, err := pgx.Connect(ctx, db.ConnStr())
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close(ctx) })

	unprotected, protected := uuid.New(), uuid.New()
	for id, protection := range map[uuid.UUID]bool{unprotected: false, protected: true} {
		_, err := conn.Exec(ctx, `INSERT INTO public.clusters (id, name, endpoint, endpoint_tls, token, deletion_protection) VALUES ($1::uuid, $1::uuid::text, 'api.example.test:5008', true, 'token', $2)`, id, protection)
		require.NoError(t, err)
	}

	require.Equal(t, http.StatusNoContent, callManagementRequestClusterDeletion(t, store, unprotected))
	require.Equal(t, http.StatusNoContent, callManagementRequestClusterDeletion(t, store, unprotected), "a repeat succeeds")
	var requested bool
	require.NoError(t, conn.QueryRow(ctx, `SELECT deletion_requested_at IS NOT NULL FROM public.clusters WHERE id = $1`, unprotected).Scan(&requested))
	require.True(t, requested)
	_, err = conn.Exec(ctx, `UPDATE public.clusters SET deletion_protection = true WHERE id = $1`, unprotected)
	require.ErrorContains(t, err, "clusters_deletion_requested_unprotected", "a requested deletion keeps protection off")

	require.Equal(t, http.StatusConflict, callManagementRequestClusterDeletion(t, store, protected))
	require.NoError(t, conn.QueryRow(ctx, `SELECT deletion_requested_at IS NOT NULL FROM public.clusters WHERE id = $1`, protected).Scan(&requested))
	require.False(t, requested)

	require.Equal(t, http.StatusNoContent, callManagementRequestClusterDeletion(t, store, uuid.New()), "an absent cluster has nothing to protect")
	require.Equal(t, http.StatusBadRequest, callManagementRequestClusterDeletion(t, store, consts.LocalClusterID))
}

func TestManagementClusterDeletionRequestWaitsForAProtectionChangeInFlight(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	store := &APIStore{db: db.SqlcClient}
	ctx := t.Context()
	conn, err := pgx.Connect(ctx, db.ConnStr())
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close(ctx) })
	cluster := uuid.New()
	_, err = conn.Exec(ctx, `INSERT INTO public.clusters (id, name, endpoint, endpoint_tls, token, deletion_protection) VALUES ($1::uuid, $1::uuid::text, 'api.example.test:5008', true, 'token', false)`, cluster)
	require.NoError(t, err)

	protecting, err := pgx.Connect(ctx, db.ConnStr())
	require.NoError(t, err)
	t.Cleanup(func() { _ = protecting.Close(ctx) })
	tx, err := protecting.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, `UPDATE public.clusters SET deletion_protection = true WHERE id = $1`, cluster)
	require.NoError(t, err)
	var protectingPID int
	require.NoError(t, tx.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&protectingPID))

	status := make(chan int, 1)
	go func() { status <- callManagementRequestClusterDeletion(t, store, cluster) }()

	require.Eventually(t, func() bool {
		var blocked bool
		err := conn.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE $1::int = ANY (pg_blocking_pids(pid)))`, protectingPID).Scan(&blocked)

		return err == nil && blocked
	}, 10*time.Second, 10*time.Millisecond)
	require.NoError(t, tx.Commit(ctx))

	require.Equal(t, http.StatusConflict, <-status)
	var requested bool
	require.NoError(t, conn.QueryRow(ctx, `SELECT deletion_requested_at IS NOT NULL FROM public.clusters WHERE id = $1`, cluster).Scan(&requested))
	require.False(t, requested)
}

func TestManagementListClusterProjectsListsLiveAssignments(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	store := &APIStore{db: db.SqlcClient, authService: &recordingCacheAuthService{}}
	clusterID := uuid.New()
	require.Equal(t, http.StatusNoContent, callManagementRegisterCluster(t, store, clusterID, managementClusterRegistration()).Code)
	assigned, deleted := testutils.CreateTestTeam(t, db), testutils.CreateTestTeam(t, db)
	testutils.CreateTestTeam(t, db)
	for _, projectID := range []uuid.UUID{assigned, deleted} {
		require.Equal(t, http.StatusNoContent, callManagementAssignProjectCluster(t, store, projectID, clusterID).Code)
	}
	require.Equal(t, http.StatusNoContent, callManagementDetachProjectCluster(t, store, deleted, clusterID).Code)

	list := func(id uuid.UUID) api.ManagementClusterProjects {
		recorder := callManagementClusterHandler(t, http.MethodGet, "/v1/management/clusters/"+id.String()+"/projects", nil,
			func(c *gin.Context) { store.ManagementListClusterProjects(c, id) })
		require.Equal(t, http.StatusOK, recorder.Code)
		var projects api.ManagementClusterProjects
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &projects))

		return projects
	}
	require.Equal(t, []uuid.UUID{assigned}, list(clusterID).ProjectIds)
	require.NotNil(t, list(uuid.New()).ProjectIds, "an absent cluster lists no projects, not null")
	require.Empty(t, list(uuid.New()).ProjectIds)
}

func TestNoTeamIsNewlyAssignedToAClusterBeingDeleted(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	store := &APIStore{db: db.SqlcClient, authService: &recordingCacheAuthService{}}
	upserts, _ := newUpsertStore(db)
	ctx := t.Context()
	clusterID := uuid.New()
	require.Equal(t, http.StatusNoContent, callManagementRegisterCluster(t, store, clusterID, managementClusterRegistration()).Code)
	assigned, late, raw := testutils.CreateTestTeam(t, db), testutils.CreateTestTeam(t, db), testutils.CreateTestTeam(t, db)
	require.Equal(t, http.StatusNoContent, callManagementAssignProjectCluster(t, store, assigned, clusterID).Code)
	require.Equal(t, http.StatusNoContent, callManagementRequestClusterDeletion(t, store, clusterID))

	refused := callManagementAssignProjectCluster(t, store, late, clusterID)
	require.Equal(t, http.StatusConflict, refused.Code)
	require.Contains(t, refused.Body.String(), string(api.ClusterAssignmentClusterDeleting))
	require.Equal(t, http.StatusNoContent, callManagementAssignProjectCluster(t, store, assigned, clusterID).Code, "an existing assignment is not a new one")
	upserted := callUpsertProject(t, upserts, uuid.New(), api.ManagementProjectUpsertRequest{Name: "Late", Slug: "late-" + uuid.NewString()[:8], Email: "late@example.test", ClusterId: &clusterID})
	require.Equal(t, http.StatusConflict, upserted.Code)
	require.Contains(t, upserted.Body.String(), "Cluster is being deleted")

	conn, err := pgx.Connect(ctx, db.ConnStr())
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close(ctx) })
	_, err = conn.Exec(ctx, `UPDATE public.teams SET cluster_id = $1 WHERE id = $2`, clusterID, raw)
	require.Equal(t, clusterDeletingConstraint, dberrors.ConstraintName(err), "every writer of a team's cluster is refused")

	listed := callManagementClusterHandler(t, http.MethodGet, "/v1/management/clusters/"+clusterID.String()+"/projects", nil,
		func(c *gin.Context) { store.ManagementListClusterProjects(c, clusterID) })
	var projects api.ManagementClusterProjects
	require.NoError(t, json.Unmarshal(listed.Body.Bytes(), &projects))
	require.Equal(t, []uuid.UUID{assigned}, projects.ProjectIds)
}

func TestAnAssignmentWaitsForADeletionRequestInFlight(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	store := &APIStore{db: db.SqlcClient, authService: &recordingCacheAuthService{}}
	ctx := t.Context()
	clusterID := uuid.New()
	require.Equal(t, http.StatusNoContent, callManagementRegisterCluster(t, store, clusterID, managementClusterRegistration()).Code)
	team := testutils.CreateTestTeam(t, db)

	deleting, err := pgx.Connect(ctx, db.ConnStr())
	require.NoError(t, err)
	t.Cleanup(func() { _ = deleting.Close(ctx) })
	tx, err := deleting.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, `SELECT deletion_protection FROM public.clusters WHERE id = $1 FOR UPDATE`, clusterID)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `UPDATE public.clusters SET deletion_requested_at = now() WHERE id = $1`, clusterID)
	require.NoError(t, err)
	var deletingPID int
	require.NoError(t, tx.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&deletingPID))

	status := make(chan int, 1)
	go func() { status <- callManagementAssignProjectCluster(t, store, team, clusterID).Code }()

	conn, err := pgx.Connect(ctx, db.ConnStr())
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close(ctx) })
	require.Eventually(t, func() bool {
		var blocked bool
		err := conn.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE $1::int = ANY (pg_blocking_pids(pid)))`, deletingPID).Scan(&blocked)

		return err == nil && blocked
	}, 10*time.Second, 10*time.Millisecond)
	require.NoError(t, tx.Commit(ctx))

	require.Equal(t, http.StatusConflict, <-status)
	var assigned *uuid.UUID
	require.NoError(t, conn.QueryRow(ctx, `SELECT cluster_id FROM public.teams WHERE id = $1`, team).Scan(&assigned))
	require.Nil(t, assigned)
}
