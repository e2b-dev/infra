package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/dashboard-api/internal/api"
	"github.com/e2b-dev/infra/packages/db/pkg/testutils"
)

// cloneTier copies the tier seeded by the migrations under a new id/name. Going
// through jsonb keeps the fixture from breaking every time a column is added to
// public.tiers, which the endpoint does not care about.
func cloneTier(t *testing.T, db *testutils.Database, id string, name string) {
	t.Helper()

	err := db.SqlcClient.TestsRawSQL(t.Context(),
		`INSERT INTO public.tiers
		 SELECT clone.*
		 FROM (SELECT * FROM public.tiers WHERE id = 'base_v1') t,
		      LATERAL jsonb_populate_record(
			      NULL::public.tiers,
			      to_jsonb(t) || jsonb_build_object('id', $1::text, 'name', $2::text)
		      ) AS clone`,
		id, name)
	require.NoError(t, err)
}

func callGetAdminTiers(t *testing.T, db *testutils.Database) (int, api.AdminTiersResponse) {
	t.Helper()

	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	ginCtx.Request = httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/tiers", nil)

	store := &APIStore{db: db.SqlcClient}
	//nolint:contextcheck // GetAdminTiers reads ctx from ginCtx.Request.Context().
	store.GetAdminTiers(ginCtx)

	var resp api.AdminTiersResponse
	if recorder.Code == http.StatusOK {
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &resp))
	}

	return recorder.Code, resp
}

func TestGetAdminTiers_ReturnsAllTiersOrderedByName(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)

	// "Base tier" (base_v1) is seeded by the migrations.
	cloneTier(t, testDB, "alpha_v1", "Alpha tier")
	cloneTier(t, testDB, "zulu_v1", "Zulu tier")

	status, resp := callGetAdminTiers(t, testDB)
	require.Equal(t, http.StatusOK, status)

	assert.Equal(t, []api.AdminTier{
		{Id: "alpha_v1", Name: "Alpha tier"},
		{Id: "base_v1", Name: "Base tier"},
		{Id: "zulu_v1", Name: "Zulu tier"},
	}, resp.Tiers)
}

func TestGetAdminTiers_SerializesEmptyListAsArray(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)

	// Teams reference tiers, so clear the dependents before emptying the table.
	require.NoError(t, testDB.SqlcClient.TestsRawSQL(t.Context(), `DELETE FROM public.teams`))
	require.NoError(t, testDB.SqlcClient.TestsRawSQL(t.Context(), `DELETE FROM public.tiers`))

	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	ginCtx.Request = httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/tiers", nil)

	store := &APIStore{db: testDB.SqlcClient}
	//nolint:contextcheck // GetAdminTiers reads ctx from ginCtx.Request.Context().
	store.GetAdminTiers(ginCtx)

	require.Equal(t, http.StatusOK, recorder.Code)
	assert.JSONEq(t, `{"tiers":[]}`, recorder.Body.String())
}
