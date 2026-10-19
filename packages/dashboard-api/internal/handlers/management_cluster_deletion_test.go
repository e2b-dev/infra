package handlers

import (
	"net/http"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

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
