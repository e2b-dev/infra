package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/launchdarkly/go-server-sdk/v7/testhelpers/ldtestdata"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
)

func TestDisableLegacyTeamMutations(t *testing.T) {
	t.Parallel()

	type route struct {
		method string
		path   string
	}

	legacyRoutes := []route{
		{http.MethodPost, "/teams"},
		{http.MethodPatch, "/teams/team-1"},
		{http.MethodPost, "/teams/team-1/members"},
		{http.MethodDelete, "/teams/team-1/members/user-1"},
		{http.MethodPost, "/admin/users/bootstrap"},
		{http.MethodDelete, "/admin/users/user-1"},
		{http.MethodPost, "/admin/teams/bootstrap"},
		{http.MethodPut, "/admin/teams/team-1/cluster"},
		{http.MethodDelete, "/admin/teams/team-1/cluster/cluster-1"},
	}

	ungatedRoutes := []route{
		{http.MethodGet, "/teams"},
		{http.MethodPost, "/admin/user-profiles/resolve"},
		{http.MethodPost, "/admin/user-profiles/by-email"},
		{http.MethodPost, "/admin/clusters"},
		{http.MethodPut, "/v1/management/projects/project-1"},
		{http.MethodPost, "/api-keys"},
	}

	for _, route := range legacyRoutes {
		t.Run("flag off "+route.method+" "+route.path, func(t *testing.T) {
			t.Parallel()

			response, reachedHandler := serveLegacyTeamMutationRoute(t, false, route.method, route.path)

			assert.Equal(t, http.StatusNoContent, response.Code)
			assert.True(t, reachedHandler)
		})

		t.Run("flag on "+route.method+" "+route.path, func(t *testing.T) {
			t.Parallel()

			response, reachedHandler := serveLegacyTeamMutationRoute(t, true, route.method, route.path)

			assert.Equal(t, http.StatusPreconditionFailed, response.Code)
			assert.JSONEq(t, `{"code":412,"message":"Legacy team mutations are no longer available. Use the workspace API."}`, response.Body.String())
			assert.False(t, reachedHandler)
		})
	}

	for _, route := range ungatedRoutes {
		t.Run("flag on remains reachable "+route.method+" "+route.path, func(t *testing.T) {
			t.Parallel()

			response, reachedHandler := serveLegacyTeamMutationRoute(t, true, route.method, route.path)

			assert.Equal(t, http.StatusNoContent, response.Code)
			assert.True(t, reachedHandler)
		})
	}
}

func serveLegacyTeamMutationRoute(t *testing.T, enabled bool, method, path string) (*httptest.ResponseRecorder, bool) {
	t.Helper()

	dataSource := ldtestdata.DataSource()
	featureFlags, err := featureflags.NewClientWithDatasource(dataSource)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, featureFlags.Close(context.WithoutCancel(t.Context())))
	})

	if enabled {
		dataSource.Update(dataSource.Flag(featureflags.DisableLegacyTeamMutationsFlag.Key()).VariationForAll(true))
	}

	router := gin.New()
	router.Use(DisableLegacyTeamMutations(featureFlags))

	reachedHandler := false
	handler := func(c *gin.Context) {
		reachedHandler = true
		c.Status(http.StatusNoContent)
	}

	router.POST("/teams", handler)
	router.PATCH("/teams/:teamID", handler)
	router.POST("/teams/:teamID/members", handler)
	router.DELETE("/teams/:teamID/members/:userId", handler)
	router.POST("/admin/users/bootstrap", handler)
	router.DELETE("/admin/users/:userId", handler)
	router.POST("/admin/teams/bootstrap", handler)
	router.PUT("/admin/teams/:teamID/cluster", handler)
	router.DELETE("/admin/teams/:teamID/cluster/:clusterID", handler)
	router.GET("/teams", handler)
	router.POST("/admin/user-profiles/resolve", handler)
	router.POST("/admin/user-profiles/by-email", handler)
	router.POST("/admin/clusters", handler)
	router.PUT("/v1/management/projects/:projectID", handler)
	router.POST("/api-keys", handler)

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), method, path, nil))

	return response, reachedHandler
}
