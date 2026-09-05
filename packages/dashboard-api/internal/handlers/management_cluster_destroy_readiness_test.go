package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/dashboard-api/internal/api"
	"github.com/e2b-dev/infra/packages/db/pkg/testutils"
	"github.com/e2b-dev/infra/packages/db/queries"
)

func TestClusterDestroyReadinessChecksOnlyActiveReferencesWithoutMutation(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	ctx := t.Context()
	teamID := createClusterAssignmentTestTeam(t, db)
	emptyCluster, activeCluster, snapshotCluster, deletedCluster := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	for _, clusterID := range []uuid.UUID{emptyCluster, activeCluster, snapshotCluster, deletedCluster} {
		require.NoError(t, db.SqlcClient.TestsRawSQL(ctx,
			`INSERT INTO public.clusters (id, name, endpoint, endpoint_tls, token) VALUES ($1::uuid, $1::uuid::text, $1::uuid::text, true, 'secret-token')`, clusterID))
	}
	require.NoError(t, db.SqlcClient.TestsRawSQL(ctx, `UPDATE public.teams SET cluster_id = $2 WHERE id = $1`, teamID, emptyCluster))
	for _, fixture := range []struct {
		clusterID uuid.UUID
		source    string
		deleted   bool
	}{
		{activeCluster, "template", false},
		{snapshotCluster, "snapshot_template", false},
		{deletedCluster, "template", true},
	} {
		templateID := testutils.CreateTestTemplate(t, db, teamID)
		require.NoError(t, db.SqlcClient.TestsRawSQL(ctx, `UPDATE public.envs SET cluster_id = $2, source = $3 WHERE id = $1`, templateID, fixture.clusterID, fixture.source))
		if fixture.deleted {
			_, err := db.SqlcClient.SoftDeleteTemplate(ctx, queries.SoftDeleteTemplateParams{TemplateID: templateID, TeamID: teamID})
			require.NoError(t, err)
		}
	}
	readState := func(t *testing.T) string {
		t.Helper()
		var state string
		require.NoError(t, db.SqlcClient.TestsRawSQLQuery(t.Context(), `SELECT jsonb_build_object(
			'clusters', (SELECT jsonb_agg(to_jsonb(c) ORDER BY id) FROM public.clusters c),
			'teams', (SELECT jsonb_agg(to_jsonb(t) ORDER BY id) FROM public.teams t),
			'envs', (SELECT jsonb_agg(to_jsonb(e) ORDER BY id) FROM public.envs e))::text`,
			func(rows pgx.Rows) error {
				require.True(t, rows.Next())

				return rows.Scan(&state)
			}))

		return state
	}
	before := readState(t)
	for _, tc := range []struct {
		name      string
		clusterID uuid.UUID
		want      int
	}{
		{"absent cluster", uuid.New(), http.StatusNoContent},
		{"assigned team without templates and other cluster active", emptyCluster, http.StatusNoContent},
		{"active template", activeCluster, http.StatusConflict},
		{"active snapshot template", snapshotCluster, http.StatusConflict},
		{"soft deleted history", deletedCluster, http.StatusNoContent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			router := gin.New()
			api.RegisterHandlers(router, &APIStore{db: db.SqlcClient})
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet,
				"/v1/management/clusters/"+tc.clusterID.String()+"/destroy-readiness", nil))
			require.Equal(t, tc.want, response.Code, response.Body.String())
			if tc.want == http.StatusConflict {
				require.JSONEq(t, `{"code":409,"message":"Cluster still has active templates or snapshots. Delete them before destroying the deployment."}`, response.Body.String())
			} else {
				require.Empty(t, response.Body.String())
			}
			require.JSONEq(t, before, readState(t))
		})
	}
}

func TestClusterDestroyReadinessRequiresAdminAuthentication(t *testing.T) {
	t.Parallel()

	swagger, err := api.GetSwagger()
	require.NoError(t, err)
	path := swagger.Paths.Value("/v1/management/clusters/{clusterID}/destroy-readiness")
	require.NotNil(t, path)
	require.NotNil(t, path.Get)
	require.NotNil(t, path.Get.Security)
	require.Equal(t, openapi3.SecurityRequirements{{"AdminJWTAuth": {}}}, *path.Get.Security)
	require.Nil(t, path.Get.Responses.Status(http.StatusNotFound))
}
